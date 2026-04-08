package converters

import (
	"bytes"
	"fmt"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"gopkg.in/yaml.v3"
)

// ToDomainPlan converts YAML bytes into a domain.Plan.
// It rejects unknown fields to prevent silent data corruption from typos
// (e.g., "symbol" instead of "identifier", "body" instead of "content").
func ToDomainPlan(data []byte) (domain.Plan, error) {
	var plan domain.Plan

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	err := decoder.Decode(&plan)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("failed to unmarshal into domain.Plan: %w", err)
	}

	return plan, nil
}
