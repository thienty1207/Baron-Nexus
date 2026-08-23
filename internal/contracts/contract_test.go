package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAcceptanceContractContainsEveryFrozenIDExactlyOnce(t *testing.T) {
	path := filepath.Join("..", "..", "acceptance", "acceptance-contract.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Contracts []struct {
			ID string `json:"id"`
		} `json:"contracts"`
		Final []string `json:"final_acceptance"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]int)
	for _, item := range contract.Contracts {
		seen[item.ID]++
	}
	for i := 1; i <= 15; i++ {
		id := "R" + itoa(i)
		if seen[id] != 1 {
			t.Fatalf("contract %s appears %d times", id, seen[id])
		}
	}
	if len(contract.Final) != 24 {
		t.Fatalf("expected 24 final acceptance IDs, got %d", len(contract.Final))
	}
	finalSeen := make(map[string]int)
	for _, id := range contract.Final {
		finalSeen[id]++
	}
	for i := 1; i <= 24; i++ {
		id := "F0" + itoa(i)
		if i >= 10 {
			id = "F" + itoa(i)
		}
		if finalSeen[id] != 1 {
			t.Fatalf("final acceptance %s appears %d times", id, finalSeen[id])
		}
	}
	loaded, err := LoadAcceptanceContract(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Contracts) != 15 || len(loaded.FinalAcceptance) != 24 {
		t.Fatalf("loaded contract shape changed: %+v", loaded)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
