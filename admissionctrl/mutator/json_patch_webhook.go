package mutator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/mxab/nacp/admissionctrl/types"
	"net/http"
	"net/url"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/api"
)

type JsonPatchWebhookMutator struct {
	name     string
	logger   hclog.Logger
	endpoint *url.URL
	method   string
}
type jsonPatchWebhookResponse struct {
	Patch    []interface{} `json:"patch"`
	Warnings []string      `json:"warnings"`
	Errors   []string      `json:"errors"`
}

func NewJsonPatchWebhookMutator(name string, endpoint string, method string, logger hclog.Logger) (*JsonPatchWebhookMutator, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	return &JsonPatchWebhookMutator{
		name:     name,
		logger:   logger,
		endpoint: u,
		method:   method,
	}, nil
}
func (j *JsonPatchWebhookMutator) Mutate(payload *types.Payload) (*api.Job, []error, error) {
	jobJson, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequest(j.method, j.endpoint.String(), bytes.NewBuffer(jobJson))
	if err != nil {
		return nil, nil, err
	}

	// Add context headers and body if available
	if payload.Context != nil {
		// Add standard headers for backward compatibility
		if payload.Context.ClientIP != "" {
			req.Header.Set("X-Forwarded-For", payload.Context.ClientIP) // Standard proxy header
			req.Header.Set("NACP-Client-IP", payload.Context.ClientIP)  // NACP specific
		}
		if payload.Context.AccessorID != "" {
			req.Header.Set("NACP-Accessor-ID", payload.Context.AccessorID)
		}
	}

	httpClient := &http.Client{}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}

	patchResponse := &jsonPatchWebhookResponse{}
	err = json.NewDecoder(res.Body).Decode(&patchResponse)
	if err != nil {
		return nil, nil, err
	}

	var warnings []error
	if len(patchResponse.Warnings) > 0 {
		j.logger.Debug("Got errors from rule", "rule", j.name, "warnings", patchResponse.Warnings, "job", payload.Job.ID)
		for _, warning := range patchResponse.Warnings {
			warnings = append(warnings, fmt.Errorf(warning))
		}
	}

	patchJson, err := json.Marshal(patchResponse.Patch)
	if err != nil {
		return nil, nil, err
	}
	patch, err := jsonpatch.DecodePatch(patchJson)
	if err != nil {
		return nil, nil, err
	}
	j.logger.Debug("Got patch fom rule", "rule", j.name, "patch", string(patchJson), "job", payload.Job.ID)
	patchedJobJson, err := patch.Apply(jobJson)

	if err != nil {
		return nil, nil, err
	}
	var patchedJob api.Job
	err = json.Unmarshal(patchedJobJson, &patchedJob)
	if err != nil {
		return nil, nil, err
	}
	return &patchedJob, warnings, nil

}
func (j *JsonPatchWebhookMutator) Name() string {
	return j.name
}
