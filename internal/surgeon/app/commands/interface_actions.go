package commands

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// AddInterface appends an interface type declaration to a file and optionally generates a mock.
func (h *ExecutePlanHandler) AddInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, error) {
	action := domain.Action{
		Action:   domain.ActionTypeAddStruct,
		FilePath: req.FilePath,
		Content:  req.Content,
	}
	if _, err := h.executeAction(ctx, action); err != nil {
		return "", err
	}

	ifaceName := extractTypeName(req.Content)

	if req.MockFile != "" && req.MockName != "" {
		mockResult, err := h.MockFromSource(ctx, req.Content, req.MockName, req.MockFile, req.FilePath)
		if err != nil {
			return "", fmt.Errorf("failed to generate mock: %w", err)
		}
		return fmt.Sprintf("Added %s to %s, %s", ifaceName, filepath.Base(req.FilePath), mockResult), nil
	}

	return fmt.Sprintf("Added %s to %s", ifaceName, filepath.Base(req.FilePath)), nil
}

// UpdateInterface replaces an existing interface type declaration and regenerates its mock.
func (h *ExecutePlanHandler) UpdateInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, error) {
	action := domain.Action{
		Action:     domain.ActionTypeUpdateStruct,
		FilePath:   req.FilePath,
		Identifier: req.Identifier,
		Content:    req.Content,
	}
	warnings, err := h.executeAction(ctx, action)
	if err != nil {
		return "", err
	}

	var fallback bool
	for _, w := range warnings {
		if strings.Contains(w, "not found in") {
			fallback = true
			break
		}
	}

	var msg string
	if fallback {
		extractedName := extractTypeName(req.Content)
		if extractedName == "interface" {
			extractedName = "new declaration"
		}
		msg = fmt.Sprintf("SUCCESS: Added %s to %s (NOTE: '--id %s' not found, content was appended as a new declaration)", extractedName, filepath.Base(req.FilePath), req.Identifier)
	} else {
		msg = fmt.Sprintf("SUCCESS: Updated %s in %s", req.Identifier, filepath.Base(req.FilePath))
	}

	if req.MockFile != "" && req.MockName != "" {
		mockResult, err := h.MockFromSource(ctx, req.Content, req.MockName, req.MockFile, req.FilePath)
		if err != nil {
			return "", fmt.Errorf("failed to regenerate mock: %w", err)
		}
		msg += ", regenerated " + mockResult
	}

	for _, w := range warnings {
		msg += fmt.Sprintf("\nWARNING (update-interface): %s", w)
	}

	return msg, nil
}

// DeleteInterface removes an interface type declaration from a file. The mock is NOT auto-deleted.
func (h *ExecutePlanHandler) DeleteInterface(ctx context.Context, req domain.InterfaceActionRequest) (string, error) {
	action := domain.Action{
		Action:     domain.ActionTypeDeleteStruct,
		FilePath:   req.FilePath,
		Identifier: req.Identifier,
	}
	if _, err := h.executeAction(ctx, action); err != nil {
		return "", err
	}
	return fmt.Sprintf("SUCCESS: Deleted %s from %s", req.Identifier, filepath.Base(req.FilePath)), nil
}

// extractTypeName extracts the type name from a Go type declaration source string.
// Returns "interface" as fallback if parsing fails.
func extractTypeName(src string) string {
	wrapped := "package p\n\n" + src
	// Simple string parsing: find "type <Name> "
	for _, line := range strings.Split(wrapped, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "type ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1]
			}
		}
	}
	return "interface"
}
