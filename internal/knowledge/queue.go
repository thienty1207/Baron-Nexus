package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/memory/tencent"
	"github.com/baron-shared-brain/baron/internal/storage"
)

type QueueHandler struct {
	Core      *tencent.Client
	Knowledge *tencent.KnowledgeClient
	Store     *storage.Store
	Isolation contracts.IsolationContext
	Registry  storage.KnowledgeRegistry
}

func NewQueueHandler(core *tencent.Client, client *tencent.KnowledgeClient, store *storage.Store, isolation contracts.IsolationContext, registry storage.KnowledgeRegistry) *QueueHandler {
	return &QueueHandler{Core: core, Knowledge: client, Store: store, Isolation: isolation, Registry: registry}
}

func (h *QueueHandler) Handle(ctx context.Context, item storage.QueueItem) (string, error) {
	if h == nil || (h.Knowledge == nil && h.Core == nil) {
		return "", errors.New("knowledge queue handler is not configured")
	}
	if item.ProjectID != h.Isolation.ProjectID {
		return "", errors.New("knowledge queue project identity mismatch")
	}
	var payload map[string]any
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return "", fmt.Errorf("decode knowledge queue payload: %w", err)
	}
	for key, want := range map[string]string{"project_id": h.Isolation.ProjectID, "team_id": h.Isolation.TeamID, "agent_id": h.Isolation.AgentID, "user_id": h.Isolation.UserID} {
		if value := strings.TrimSpace(payloadString(payload, key)); value != "" && value != want {
			return "", fmt.Errorf("knowledge queue %s mismatch", key)
		}
	}
	var requestID string
	switch item.Operation {
	case storage.QueueOperationWikiIngest:
		wikiID := firstNonEmpty(payloadString(payload, "wiki_id"), h.Registry.WikiID)
		if wikiID == "" {
			return "", errors.New("knowledge queue has no wiki_id")
		}
		if payloadString(payload, "action") == "wiki_readiness" {
			asset, err := h.Knowledge.WaitWikiReady(ctx, h.Isolation, wikiID, 50*time.Millisecond)
			if err != nil {
				return "", err
			}
			requestID = firstNonEmpty(asset.ID, asset.WikiID, item.IdempotencyKey)
			h.Registry.WikiStatus = firstNonEmpty(asset.Status, "ready")
			h.Registry.WikiIngestStatus = "ready"
			break
		}
		result, err := h.Knowledge.IngestWiki(ctx, h.Isolation, wikiID)
		if err != nil {
			return "", err
		}
		requestID = result.RequestID
		h.Registry.WikiIngestStatus = "pending"
	case storage.QueueOperationCodeGraphSync:
		graphID := firstNonEmpty(payloadString(payload, "code_graph_id"), h.Registry.CodeGraphID)
		if graphID == "" {
			return "", errors.New("knowledge queue has no code_graph_id")
		}
		if payloadString(payload, "action") == "codegraph_readiness" {
			asset, err := h.Knowledge.WaitCodeGraphReady(ctx, h.Isolation, graphID, 50*time.Millisecond)
			if err != nil {
				return "", err
			}
			requestID = firstNonEmpty(asset.ID, asset.CodeGraphID, item.IdempotencyKey)
			h.Registry.CodeGraphStatus = firstNonEmpty(asset.Status, "ready")
			h.Registry.CodeGraphSyncStatus = "ready"
			break
		}
		result, err := h.Knowledge.SyncCodeGraph(ctx, h.Isolation, graphID)
		if err != nil {
			return "", err
		}
		requestID = result.RequestID
		h.Registry.CodeGraphSyncStatus = "pending"
	case storage.QueueOperationCoreUpdate:
		if h.Core == nil {
			return "", errors.New("core update requires the MemoryCore client")
		}
		result, err := h.Core.WriteCore(ctx, h.Isolation, map[string]any{"content": normalizedMemoryContent(payload)})
		if err != nil {
			return "", err
		}
		requestID = firstNonEmpty(result.RequestID, item.IdempotencyKey)
	case storage.QueueOperationScenarioUpdate:
		if h.Core == nil {
			return "", errors.New("scenario update requires the MemoryCore client")
		}
		content := normalizedMemoryContent(payload)
		path := normalizedScenarioPath(payload, item.IdempotencyKey)
		result, err := h.Core.WriteScenario(ctx, h.Isolation, map[string]any{
			"path":    path,
			"content": content,
		})
		if err != nil {
			if !isMissingScenarioFile(err) {
				return "", err
			}
			// Tencent's v3 scenario/write is intentionally update-only: the
			// service returns 404 until its extractor has created a scene file.
			// Preserve the continuity summary in L0 immediately so a fresh
			// project never accumulates a permanent retry merely because L2 is
			// not seeded yet.
			receipt, fallbackErr := h.Core.Capture(ctx, h.Isolation, contracts.MemoryRecord{
				ProjectID: h.Isolation.ProjectID, SourceClient: contracts.ClientSystem,
				SessionID: payloadString(payload, "session_id"), Kind: "continuity_summary",
				Content: content, Metadata: map[string]string{"scenario_path": path},
			}, item.IdempotencyKey)
			if fallbackErr != nil {
				return "", fmt.Errorf("scenario update failed and L0 fallback failed: %w", fallbackErr)
			}
			requestID = firstNonEmpty(receipt.RequestID, item.IdempotencyKey)
			break
		}
		requestID = firstNonEmpty(result.RequestID, item.IdempotencyKey)
	case storage.QueueOperationSkillUpdate:
		if h.Core == nil {
			return "", errors.New("skill update requires the MemoryCore client")
		}
		if skillID := payloadString(payload, "skill_id"); skillID != "" {
			result, err := h.Core.UpdateSkill(ctx, h.Isolation, skillID, payload)
			if err != nil {
				return "", err
			}
			requestID = firstNonEmpty(result.RequestID, item.IdempotencyKey)
		} else {
			skill, err := h.Core.CreateSkill(ctx, h.Isolation, payloadString(payload, "name"), payloadString(payload, "content"))
			if err != nil {
				return "", err
			}
			requestID = firstNonEmpty(skill.ID, skill.SkillID, item.IdempotencyKey)
		}
	case storage.QueueOperationMetadataRepair:
		if h.Core == nil {
			return "", errors.New("metadata repair requires the MemoryCore client")
		}
		knowledgeID := firstNonEmpty(payloadString(payload, "wiki_id"), payloadString(payload, "code_graph_id"))
		if knowledgeID == "" {
			return "", errors.New("metadata repair has no knowledge asset ID")
		}
		typeName := "wiki"
		assetID := knowledgeID
		if payloadString(payload, "code_graph_id") != "" && payloadString(payload, "wiki_id") == "" {
			typeName = "code-graph"
			assetID = knowledgeID
		}
		metadata, err := h.Core.CreateKnowledgeMetadata(ctx, h.Isolation, tencent.KnowledgeMetadata{KnowledgeID: knowledgeID, Type: typeName, ServiceURL: h.Registry.ServiceURL, TeamID: h.Isolation.TeamID, UserID: h.Isolation.UserID, AgentID: h.Isolation.AgentID, ProjectID: h.Isolation.ProjectID, WikiID: assetID})
		if typeName == "code-graph" {
			metadata, err = h.Core.CreateKnowledgeMetadata(ctx, h.Isolation, tencent.KnowledgeMetadata{KnowledgeID: knowledgeID, Type: typeName, ServiceURL: h.Registry.ServiceURL, TeamID: h.Isolation.TeamID, UserID: h.Isolation.UserID, AgentID: h.Isolation.AgentID, ProjectID: h.Isolation.ProjectID, CodeGraphID: assetID})
		}
		if err != nil {
			return "", err
		}
		requestID = firstNonEmpty(metadata.KnowledgeID, metadata.ID)
	default:
		return "", fmt.Errorf("unsupported knowledge queue operation %q", item.Operation)
	}
	if h.Store != nil {
		h.Registry.LastError = ""
		if err := h.Store.UpsertKnowledgeRegistry(ctx, h.Registry); err != nil {
			return "", err
		}
	}
	return firstNonEmpty(requestID, item.IdempotencyKey), nil
}

func payloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// normalizedMemoryContent repairs older queue records that predate the
// Tencent v3 core/scenario schemas. Hook payloads historically stored the
// useful text under summary, while the remote write contracts require an
// explicit content string. Keeping this normalization at the queue boundary
// lets already-persisted records drain without manual database edits.
func normalizedMemoryContent(payload map[string]any) string {
	for _, key := range []string{"content", "summary", "message", "text"} {
		if value := strings.TrimSpace(payloadString(payload, key)); value != "" {
			return value
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil || strings.TrimSpace(string(encoded)) == "" {
		return "Baron Nexus continuity update"
	}
	return string(encoded)
}

// normalizedScenarioPath creates one stable, traversal-safe L2 file per
// session. A repeated session update therefore versions the same remote file,
// while legacy queue rows without a session still get a deterministic path.
func normalizedScenarioPath(payload map[string]any, fallback string) string {
	segment := firstNonEmpty(payloadString(payload, "session_id"), fallback, "session")
	var builder strings.Builder
	for _, character := range segment {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '-' || character == '_' {
			builder.WriteRune(character)
		} else if builder.Len() == 0 || !strings.HasSuffix(builder.String(), "_") {
			builder.WriteByte('_')
		}
	}
	clean := strings.Trim(builder.String(), "_")
	if clean == "" {
		clean = "session"
	}
	return "baron/sessions/" + clean + ".md"
}

func isMissingScenarioFile(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "scenario file not found") || strings.Contains(message, "http 404") && strings.Contains(message, "scenario")
}
