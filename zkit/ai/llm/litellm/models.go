package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const maxModelsBytes = 8 << 20

// ErrModelsResponse identifies an invalid or oversized ordinary-key model list.
// Invalid response contents are never included in the returned diagnostic.
var ErrModelsResponse = errors.New("invalid litellm models response")

// Models lists aliases visible to the configured ordinary key via /v1/models.
// It does not require admin access, query /model/info, infer metadata, or cache.
// IDs retain their exact spelling; duplicate IDs are kept only at their first
// occurrence. Each successful call returns an independently owned slice. An
// empty data array is valid; absent/null data and empty/control-character IDs
// and unpaired JSON surrogate escapes are invalid. Bodies are limited to 8 MiB
// and closed before this call returns.
func (p *Provider) Models(ctx context.Context) ([]llm.ModelID, error) {
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.baseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, errors.New("litellm models: invalid gateway URL")
	}
	req.Header.Set("Accept", "application/json")
	response, err := p.client(llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS).Do(req)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return nil, fmt.Errorf("litellm models: %w", completionError(ctx, err))
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxModelsBytes+1))
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return nil, fmt.Errorf("read litellm models: %w", ErrModelsResponse)
	}
	if len(data) > maxModelsBytes || !utf8.Valid(data) {
		return nil, ErrModelsResponse
	}
	var envelope struct {
		Data []struct {
			ID modelAlias `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Data == nil {
		return nil, ErrModelsResponse
	}
	ids := make([]llm.ModelID, 0, len(envelope.Data))
	seen := make(map[string]struct{}, len(envelope.Data))
	for _, model := range envelope.Data {
		if strings.TrimSpace(string(model.ID)) == "" || strings.ContainsFunc(string(model.ID), unicode.IsControl) {
			return nil, ErrModelsResponse
		}
		if _, duplicate := seen[string(model.ID)]; duplicate {
			continue
		}
		seen[string(model.ID)] = struct{}{}
		ids = append(ids, llm.ModelID(model.ID))
	}
	return ids, nil
}

// modelAlias validates JSON's UTF-16 escapes before encoding/json can replace
// unpaired surrogates with U+FFFD and change (or collapse) deployment identity.
type modelAlias string

func (id *modelAlias) UnmarshalJSON(data []byte) error {
	var decoded string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ErrModelsResponse
	}
	for i := 1; i < len(data)-1; i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if data[i] != 'u' {
			continue
		}
		unit, _ := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		i += 4
		if unit >= 0xDC00 && unit <= 0xDFFF {
			return ErrModelsResponse
		}
		if unit < 0xD800 || unit > 0xDBFF {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return ErrModelsResponse
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xDC00 || low > 0xDFFF {
			return ErrModelsResponse
		}
		i += 6
	}
	*id = modelAlias(decoded)
	return nil
}
