package rag

import "testing"

func TestValidateEmbeddingDims(t *testing.T) {
	vec := func(n int) []float32 { return make([]float32, n) }

	tests := []struct {
		name     string
		chunks   []Chunk
		expected int
		wantDim  int
		wantErr  bool
	}{
		{
			name:    "uniform with unembedded skipped",
			chunks:  []Chunk{{ID: "a", Embedding: vec(1024)}, {ID: "b"}, {ID: "c", Embedding: vec(1024)}},
			wantDim: 1024,
		},
		{
			name:    "matches expected",
			chunks:  []Chunk{{ID: "a", Embedding: vec(1024)}},
			wantDim: 1024,
			// expected set below
		},
		{
			name:    "mismatched dims",
			chunks:  []Chunk{{ID: "a", Embedding: vec(1024)}, {ID: "b", Embedding: vec(768)}},
			wantErr: true,
		},
		{
			name:    "no embeddings",
			chunks:  []Chunk{{ID: "a"}, {ID: "b"}},
			wantErr: true,
		},
		{
			name:     "wrong model vs expected",
			chunks:   []Chunk{{ID: "a", Embedding: vec(768)}},
			expected: 1024,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// "matches expected" exercises the expected==dim path.
			expected := tt.expected
			if tt.name == "matches expected" {
				expected = 1024
			}
			dim, err := validateEmbeddingDims(tt.chunks, expected)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got dim=%d", dim)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dim != tt.wantDim {
				t.Fatalf("dim = %d, want %d", dim, tt.wantDim)
			}
		})
	}
}
