package jev

// schemaFor returns the exact strict JSON Schema sent to the Responses API.
// Every object has additionalProperties=false and every property is required,
// as required by OpenAI Structured Outputs.
func schemaFor(name string) map[string]any {
	evidenceIDs := array(stringSchema())
	confidence := map[string]any{"type": "number", "minimum": 0, "maximum": 1}
	switch name {
	case "classify_incident":
		class := object(map[string]any{
			"class":        enum("RESOURCE_EXHAUSTION", "BAD_DEPLOYMENT", "DEPENDENCY_FAILURE", "TRAFFIC_SPIKE", "CONFIGURATION_ERROR", "UNKNOWN"),
			"confidence":   confidence,
			"evidence_ids": evidenceIDs,
		}, "class", "confidence", "evidence_ids")
		return object(map[string]any{"classes": array(class), "rationale": stringSchema()}, "classes", "rationale")
	case "select_next_action":
		return object(map[string]any{
			"action":            enum("INVESTIGATE_MORE", "TEST_REMEDIATION", "REMEDIATE", "ESCALATE"),
			"rationale":         stringSchema(),
			"confidence":        confidence,
			"evidence_ids":      evidenceIDs,
			"required_evidence": array(stringSchema()),
		}, "action", "rationale", "confidence", "evidence_ids", "required_evidence")
	case "classify_remediation_risk":
		return object(map[string]any{
			"risk":         enum("LOW", "MEDIUM", "HIGH"),
			"action_ref":   stringSchema(),
			"risk_factors": array(stringSchema()),
			"evidence_ids": evidenceIDs,
		}, "risk", "action_ref", "risk_factors", "evidence_ids")
	case "verify_recovery":
		return object(map[string]any{
			"outcome":          enum("RECOVERED", "PARTIALLY_RECOVERED", "NOT_RECOVERED", "UNCERTAIN"),
			"confidence":       confidence,
			"evidence_ids":     evidenceIDs,
			"observed_signals": array(stringSchema()),
		}, "outcome", "confidence", "evidence_ids", "observed_signals")
	default:
		panic("unknown decision schema " + name)
	}
}

func object(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func array(item map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": item}
}

func stringSchema() map[string]any { return map[string]any{"type": "string"} }

func enum(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}
