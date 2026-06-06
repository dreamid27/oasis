package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nevindra/oasis/a2a/a2atest"
)

func TestCardFromAgent(t *testing.T) {
	ag := a2atest.NewEchoAgent("researcher", "Finds and summarizes sources")
	srv := NewServer(ag,
		WithSkill(AgentSkill{ID: "research", Name: "Research", Description: "Web research"}),
		WithSecurityScheme("bearer", SecurityScheme{HTTPAuth: &HTTPAuthSecurityScheme{Scheme: "bearer"}}),
		WithURL("https://agents.example.com/a2a"),
	)
	card := srv.Card()
	if card.Name != "researcher" || card.Description != "Finds and summarizes sources" {
		t.Errorf("card identity: %+v", card)
	}
	// WithURL advertises both transports this server actually serves.
	if len(card.SupportedInterfaces) != 2 {
		t.Fatalf("supportedInterfaces: %+v", card.SupportedInterfaces)
	}
	bindings := map[string]bool{}
	for _, ifc := range card.SupportedInterfaces {
		if ifc.URL != "https://agents.example.com/a2a" {
			t.Errorf("interface URL: %q", ifc.URL)
		}
		bindings[ifc.ProtocolBinding] = true
	}
	if !bindings[BindingJSONRPC] || !bindings[BindingHTTPJSON] {
		t.Errorf("bindings: %v", bindings)
	}
	if !card.Capabilities.Streaming {
		t.Error("streaming must default true")
	}
	if len(card.Skills) != 1 || card.Skills[0].ID != "research" {
		t.Errorf("skills: %+v", card.Skills)
	}
	if _, ok := card.SecuritySchemes["bearer"]; !ok {
		t.Errorf("security schemes: %+v", card.SecuritySchemes)
	}
}

func TestWellKnownCardEndpoint(t *testing.T) {
	srv := NewServer(a2atest.NewEchoAgent("echo", "echoes"))
	ts := httptest.NewServer(srv)
	defer ts.Close()
	resp, err := http.Get(ts.URL + WellKnownCardPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var card AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	if card.Name != "echo" {
		t.Errorf("card.Name = %q", card.Name)
	}
}
