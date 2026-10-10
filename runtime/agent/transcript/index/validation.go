package index

import (
	"fmt"
	"unicode/utf8"
)

func validateEntry(entry MessageEntry) error {
	if entry.ID == 0 {
		return fmt.Errorf("index: entry ID must be positive")
	}
	if entry.Call != nil || entry.NonText != 0 || entry.Keep || entry.Origin != nil {
		return fmt.Errorf("index: later-ticket fields must be zero")
	}
	switch entry.Kind {
	case Message, Assistant, ToolResult, Clarification, TypedInput:
		if entry.Structural != nil {
			return fmt.Errorf("index: unexpected structural parameters")
		}
	case Structural:
		if entry.Structural == nil || entry.Hash != "" {
			return fmt.Errorf("index: structural entry requires parameters and no content hash")
		}
		if err := validateStructural(*entry.Structural); err != nil {
			return err
		}
	default:
		return fmt.Errorf("index: unknown entry kind %q", entry.Kind)
	}
	if entry.Result != nil {
		if err := validateText(entry.Result.Preview); err != nil {
			return err
		}
		if entry.Result.Error != nil {
			return validateText(*entry.Result.Error)
		}
	}
	return nil
}

func validateStructural(s StructuralEntry) error {
	if s.ProtocolVersion != TemplateProtocolVersion {
		return fmt.Errorf("index: unsupported template protocol %d", s.ProtocolVersion)
	}
	switch s.Kind {
	case UnknownTool, ConfirmationDenial, SynthesizedError:
		if len(s.Text) != 1 {
			return fmt.Errorf("index: %s requires one text parameter", s.Kind)
		}
	case PolicyDenial:
		if len(s.Text) > 1 {
			return fmt.Errorf("index: policy denial accepts at most one parameter")
		}
	case Reminders:
	case Cancellation, FinalizeResults, FinalizeHint, HideBookkeeping:
		if len(s.Text) != 0 {
			return fmt.Errorf("index: %s accepts no text parameters", s.Kind)
		}
	default:
		return fmt.Errorf("index: unknown structural kind %q", s.Kind)
	}
	for _, param := range s.Text {
		if err := validateText(param); err != nil {
			return err
		}
	}
	if len(s.Reason) > MaxInlineTextBytes {
		return fmt.Errorf("index: oversized structural reason")
	}
	return nil
}

func validateText(param TextParameter) error {
	if param.Hash != "" && param.Text != "" {
		return fmt.Errorf("index: text parameter has both inline text and a hash")
	}
	if len(param.Text) > MaxInlineTextBytes || !utf8.ValidString(param.Text) {
		return fmt.Errorf("index: text must be valid UTF-8 within %d bytes; store larger text with RecordContent", MaxInlineTextBytes)
	}
	return nil
}
