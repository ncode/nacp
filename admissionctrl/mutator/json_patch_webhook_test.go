package mutator

import (
	"fmt"
	"github.com/mxab/nacp/admissionctrl/types"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJsonPatchMutator(t *testing.T) {

	tt := []struct {
		name string

		job          *api.Job
		endpointPath string
		method       string

		response  []byte
		wantErr   error
		wantWarns []error
		wantJob   *api.Job
	}{
		{
			name:         "empty response",
			endpointPath: "/mutate",
			method:       "POST",

			response: []byte(`{}`),

			job:       &api.Job{},
			wantErr:   nil,
			wantWarns: nil,
			wantJob:   &api.Job{},
		},
		{
			name:         "patch",
			endpointPath: "/mutate",
			method:       "POST",

			response: []byte(`{
				"patch": [
					{"op": "add", "path": "/Meta", "value": {"foo": "bar"}}
				]
			}`),

			job: &api.Job{},

			wantErr:   nil,
			wantWarns: nil,
			wantJob:   &api.Job{Meta: map[string]string{"foo": "bar"}},
		},
		{
			name:         "with warnings",
			endpointPath: "/mutate",
			method:       "POST",

			response: []byte(`{
				"warnings": [
					"Warning 1",
					"Warning 2"
				]
			}`),

			job: &api.Job{},

			wantErr:   nil,
			wantWarns: []error{fmt.Errorf("Warning 1"), fmt.Errorf("Warning 2")},
			wantJob:   &api.Job{},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {

			webhookCalled := false

			webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				webhookCalled = true
				assert.Equal(t, tc.endpointPath, r.URL.Path)
				assert.Equal(t, tc.method, r.Method)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write(tc.response)

			}))
			defer webhookServer.Close()

			mutator, err := NewJsonPatchWebhookMutator(tc.name, webhookServer.URL+tc.endpointPath, tc.method, hclog.NewNullLogger())
			require.NoError(t, err)

			payload := &types.Payload{Job: tc.job}
			job, warnings, err := mutator.Mutate(payload)

			require.True(t, webhookCalled)
			assert.Equal(t, tc.wantErr, err)
			assert.Equal(t, tc.wantWarns, warnings)
			assert.Equal(t, tc.wantJob, job)

		})
	}
}
