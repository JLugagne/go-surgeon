package converters

import (
	"fmt"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"gopkg.in/yaml.v3"
)

// ToDomainPlan converts YAML bytes into a domain.Plan.
func ToDomainPlan(data []byte) (domain.Plan, error) {
	var plan domain.Plan

	err := yaml.Unmarshal(data, &plan)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("failed to unmarshal into domain.Plan: %w", err)
	}

	return plan, nil
}
