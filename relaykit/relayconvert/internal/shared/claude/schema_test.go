package claude

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlattenInputSchemaUnions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema map[string]any
		want   map[string]any
		change bool
	}{
		{
			name: "anyOf merges branch properties and intersects required lists",
			schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"shared": map[string]any{"type": "string"},
				},
				"anyOf": []any{
					map[string]any{
						"properties": map[string]any{
							"shared": map[string]any{"type": "number"},
							"a":      map[string]any{"type": "string"},
						},
						"required": []any{"a"},
					},
					map[string]any{
						"properties": map[string]any{
							"b": map[string]any{"type": "integer"},
						},
						"required": []any{"b"},
					},
				},
			},
			want: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"shared": map[string]any{"type": "string"},
					"a":      map[string]any{"type": "string"},
					"b":      map[string]any{"type": "integer"},
				},
			},
			change: true,
		},
		{
			name: "allOf unions branch required lists",
			schema: map[string]any{
				"type": "object",
				"allOf": []any{
					map[string]any{
						"properties": map[string]any{"a": map[string]any{"type": "string"}},
						"required":   []any{"a"},
					},
					map[string]any{
						"properties": map[string]any{"b": map[string]any{"type": "integer"}},
						"required":   []any{"b"},
					},
				},
			},
			want: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"type": "string"},
					"b": map[string]any{"type": "integer"},
				},
				"required": []string{"a", "b"},
			},
			change: true,
		},
		{
			name: "non-object branch falls back to an empty permissive object",
			schema: map[string]any{
				"anyOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"enum": []any{"a", "b"}},
				},
			},
			want: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			change: true,
		},
		{
			name: "anyOf keeps top-level required entries",
			schema: map[string]any{
				"type":     "object",
				"required": []any{"base"},
				"anyOf": []any{
					map[string]any{"required": []any{"a"}},
					map[string]any{"required": []any{"b"}},
				},
			},
			want: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []string{"base"},
			},
			change: true,
		},
		{
			name: "schema without a top-level union is unchanged",
			schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"a": map[string]any{"type": "string"}},
				"required":   []any{"a"},
			},
			want: map[string]any{
				"type":       "object",
				"properties": map[string]any{"a": map[string]any{"type": "string"}},
				"required":   []any{"a"},
			},
			change: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			changed := FlattenInputSchemaUnions(tt.schema)
			assert.Equal(t, tt.change, changed)
			assert.Equal(t, tt.want, tt.schema)
		})
	}
}

func TestFlattenInputSchemaUnionsHandlesDefensiveShapes(t *testing.T) {
	t.Parallel()

	t.Run("[ ]map[string]any branch slice", func(t *testing.T) {
		t.Parallel()

		schema := map[string]any{
			"oneOf": []map[string]any{
				{"properties": map[string]any{"a": map[string]any{"type": "string"}}},
				nil,
			},
		}
		require.True(t, FlattenInputSchemaUnions(schema))
		assert.Equal(t, map[string]any{
			"type":       "object",
			"properties": map[string]any{"a": map[string]any{"type": "string"}},
		}, schema)
	})

	t.Run("non-slice union and lowercase object type", func(t *testing.T) {
		t.Parallel()

		schema := map[string]any{
			"anyOf": map[string]any{"not": "a slice"},
		}
		require.True(t, FlattenInputSchemaUnions(schema))
		assert.Equal(t, "object", schema["type"])
		assert.Equal(t, map[string]any{}, schema["properties"])

		typed := map[string]any{
			"type": "OBJECT",
			"anyOf": []any{
				map[string]any{"properties": map[string]any{"b": map[string]any{"type": "string"}}},
			},
		}
		require.True(t, FlattenInputSchemaUnions(typed))
		assert.Equal(t, map[string]any{
			"type":       "OBJECT",
			"properties": map[string]any{"b": map[string]any{"type": "string"}},
		}, typed)
	})

	t.Run("branch string required fields are honored", func(t *testing.T) {
		t.Parallel()

		schema := map[string]any{
			"allOf": []any{
				map[string]any{
					"type":     "object",
					"required": []string{"a", "a"},
				},
				map[string]any{
					"type":     "object",
					"required": []string{"b"},
				},
			},
		}
		require.True(t, FlattenInputSchemaUnions(schema))
		assert.Equal(t, []string{"a", "b"}, schema["required"])
	})
}
