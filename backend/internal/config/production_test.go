package config

import "testing"

func TestProductionExecutorDisabledByDefault(t *testing.T) {
	t.Setenv("KNULL_PRODUCTION_EXECUTION_ENABLED", "")
	cfg, err := Load()
	if err != nil || cfg.ProductionExecutionEnabled {
		t.Fatalf("config: %+v %v", cfg, err)
	}
}

func TestProductionExecutorNeedsExactScope(t *testing.T) {
	t.Setenv("KNULL_PRODUCTION_EXECUTION_ENABLED", "true")
	t.Setenv("KNULL_PRODUCTION_CLUSTER", "")
	if _, err := Load(); err == nil {
		t.Fatal("enabled production executor accepted without scope")
	}
	t.Setenv("KNULL_PRODUCTION_EXECUTION_ENABLED", "definitely")
	if _, err := Load(); err == nil {
		t.Fatal("malformed production flag accepted")
	}
}
