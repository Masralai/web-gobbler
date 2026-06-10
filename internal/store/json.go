package store

import (
	"encoding/json"

	"github.com/Masralai/web-gobbler/internal/scraper"
)

func resultToJSON(r *scraper.Result) *string {
	if r == nil {
		return nil
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil
	}
	s := string(data)
	return &s
}

func parseResultJSON(s *string) *scraper.Result {
	if s == nil || *s == "" {
		return nil
	}
	var r scraper.Result
	if err := json.Unmarshal([]byte(*s), &r); err != nil {
		return nil
	}
	return &r
}

func optionsToJSON(o *JobOptions) *string {
	if o == nil {
		return nil
	}
	data, err := json.Marshal(o)
	if err != nil {
		return nil
	}
	s := string(data)
	return &s
}

func parseOptionsJSON(s *string) *JobOptions {
	if s == nil || *s == "" {
		return nil
	}
	var o JobOptions
	if err := json.Unmarshal([]byte(*s), &o); err != nil {
		return nil
	}
	return &o
}
