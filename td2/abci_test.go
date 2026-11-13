package tenderduty

import (
	"context"
	"testing"
)

func TestQueryABCI(t *testing.T) {
	tests := []struct {
		name      string
		result    fakeABCIResult
		want      string
		wantErr   error
		errSubstr string
	}{
		{"transport error is wrapped", errResult(errRateLimited), "", errRateLimited, slashingParamsPath},
		{"nil response", fakeABCIResult{}, "", errEmptyABCIResponse, ""},
		{"non-zero code includes log", codeResult(18, "invalid request"), "", errABCIQueryFailed, "code 18"},
		{"nil value", okResult(nil), "", errEmptyABCIResponse, ""},
		{"success", okResult([]byte("value")), "value", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeABCIQuerier{results: map[string]fakeABCIResult{slashingParamsPath: tt.result}}
			got, err := queryABCI(context.Background(), client, slashingParamsPath, nil)
			assertError(t, err, tt.wantErr, tt.errSubstr)
			if string(got) != tt.want {
				t.Errorf("queryABCI() = %q, want %q", got, tt.want)
			}
		})
	}
}
