package metrics

import (
	"testing"

	"github.com/prometheus/common/model"
)

func TestExtractMTUReturnsLatestSample(t *testing.T) {
	value := model.Matrix{
		&model.SampleStream{
			Values: []model.SamplePair{
				{Value: model.SampleValue(1400)},
				{Value: model.SampleValue(1500)},
			},
		},
	}

	mtu, err := extractMTU(value)
	if err != nil {
		t.Fatalf("extractMTU returned unexpected error: %v", err)
	}
	if mtu != 1500 {
		t.Fatalf("extractMTU returned %d, expected 1500", mtu)
	}
}

func TestExtractMTUSkipsEmptyStreams(t *testing.T) {
	value := model.Matrix{
		nil,
		&model.SampleStream{},
		&model.SampleStream{
			Values: []model.SamplePair{
				{Value: model.SampleValue(9000)},
			},
		},
	}

	mtu, err := extractMTU(value)
	if err != nil {
		t.Fatalf("extractMTU returned unexpected error: %v", err)
	}
	if mtu != 9000 {
		t.Fatalf("extractMTU returned %d, expected 9000", mtu)
	}
}

func TestExtractMTURejectsEmptyResponse(t *testing.T) {
	testCases := []struct {
		name  string
		value model.Value
	}{
		{
			name:  "empty matrix",
			value: model.Matrix{},
		},
		{
			name: "streams without samples",
			value: model.Matrix{
				&model.SampleStream{},
				&model.SampleStream{Values: []model.SamplePair{}},
			},
		},
		{
			name:  "unexpected type",
			value: model.Vector{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := extractMTU(tc.value); err == nil {
				t.Fatal("extractMTU succeeded, expected an error")
			}
		})
	}
}
