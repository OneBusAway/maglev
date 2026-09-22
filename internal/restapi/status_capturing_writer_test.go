package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusCapturingWriter(t *testing.T) {
	tests := []struct {
		name           string
		handler        func(t *testing.T, w *statusCapturingWriter)
		expectedStatus int
	}{
		{
			name: "preserves first status",
			handler: func(t *testing.T, w *statusCapturingWriter) {
				w.WriteHeader(http.StatusNotFound)
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name: "write commits 200",
			handler: func(t *testing.T, w *statusCapturingWriter) {
				_, err := w.Write([]byte("ok"))
				require.NoError(t, err)

				w.WriteHeader(http.StatusInternalServerError)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "records explicit status",
			handler: func(t *testing.T, w *statusCapturingWriter) {
				w.WriteHeader(http.StatusCreated)
			},
			expectedStatus: http.StatusCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			w := newStatusCapturingWriter(rec)

			tt.handler(t, w)

			assert.Equal(t, tt.expectedStatus, w.statusCode)
			assert.Equal(t, tt.expectedStatus, rec.Code)
		})
	}
}
