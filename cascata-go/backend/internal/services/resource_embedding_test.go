package services

import (
	"testing"

	"cascata-backend/internal/utils"
)

func TestParseSelect(t *testing.T) {
	parser := NewResourceEmbeddingParser(nil)

	tests := []struct {
		name     string
		input    string
		wantErr  bool
		expected int // number of fields expected
	}{
		{
			name:     "simple columns",
			input:    "id,name,price",
			wantErr:  false,
			expected: 3,
		},
		{
			name:     "wildcard",
			input:    "*",
			wantErr:  false,
			expected: 1,
		},
		{
			name:     "empty",
			input:    "",
			wantErr:  false,
			expected: 1,
		},
		{
			name:     "simple embedding",
			input:    "id,product_catalog(name)",
			wantErr:  false,
			expected: 2,
		},
		{
			name:     "multiple columns in embedding",
			input:    "id,product_catalog(name,brand)",
			wantErr:  false,
			expected: 2,
		},
		{
			name:     "alias",
			input:    "id:identifier,name:title",
			wantErr:  false,
			expected: 2,
		},
		{
			name:     "nested embedding",
			input:    "product_catalog(category(name))",
			wantErr:  false,
			expected: 1,
		},
		{
			name:     "invalid table name",
			input:    "invalid-table(name)",
			wantErr:  true,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields, err := parser.ParseSelect(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSelect() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(fields) != tt.expected {
				t.Errorf("ParseSelect() got %d fields, want %d", len(fields), tt.expected)
			}
		})
	}
}

func TestParseField(t *testing.T) {
	parser := NewResourceEmbeddingParser(nil)

	tests := []struct {
		name    string
		input   string
		wantErr bool
		isEmbed bool
	}{
		{
			name:    "simple column",
			input:   "id",
			wantErr: false,
			isEmbed: false,
		},
		{
			name:    "embedded resource",
			input:   "product_catalog(name)",
			wantErr: false,
			isEmbed: true,
		},
		{
			name:    "embedded with multiple columns",
			input:   "product_catalog(name,brand,price)",
			wantErr: false,
			isEmbed: true,
		},
		{
			name:    "alias on simple column",
			input:   "id:identifier",
			wantErr: false,
			isEmbed: false,
		},
		{
			name:    "alias on embedded",
			input:   "product_catalog(name):catalog",
			wantErr: false,
			isEmbed: true,
		},
		{
			name:    "invalid identifier",
			input:   "invalid-name",
			wantErr: true,
			isEmbed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field, err := parser.parseField(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseField() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && field.IsEmbedded != tt.isEmbed {
				t.Errorf("parseField() IsEmbedded = %v, want %v", field.IsEmbedded, tt.isEmbed)
			}
		})
	}
}

func TestIsValidIdentifier(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"id", true},
		{"user_id", true},
		{"userId", true},
		{"_id", true},
		{"id123", true},
		{"", false},
		{"invalid-name", false},
		{"invalid.name", false},
		{"invalid name", false},
		{"123id", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := isValidIdentifier(tt.input); got != tt.want {
				t.Errorf("isValidIdentifier(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestQuoteIdentifier(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"id", `"id"`},
		{"user_id", `"user_id"`},
		{"my\"column", `"my""column"`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := utils.QuoteId(tt.input); got != tt.want {
				t.Errorf("utils.QuoteId(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
