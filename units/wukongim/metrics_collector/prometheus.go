package metrics_collector

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type metricSample struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
}

type metricFilter struct {
	include []*regexp.Regexp
	exclude []*regexp.Regexp
}

func newMetricFilter(spec collectorSpec) (metricFilter, error) {
	include, err := compileRegexps("include", spec.Include)
	if err != nil {
		return metricFilter{}, err
	}
	exclude, err := compileRegexps("exclude", spec.Exclude)
	if err != nil {
		return metricFilter{}, err
	}
	return metricFilter{include: include, exclude: exclude}, nil
}

func parsePrometheusText(data []byte, filter metricFilter) ([]metricSample, int64) {
	lines := strings.Split(string(data), "\n")
	samples := make([]metricSample, 0, len(lines))
	var parseErrors int64
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sample, ok := parsePrometheusLine(line)
		if !ok {
			parseErrors++
			continue
		}
		if !filter.match(sample.Name) {
			continue
		}
		samples = append(samples, sample)
	}
	return samples, parseErrors
}

func compileRegexps(field string, patterns []string) ([]*regexp.Regexp, error) {
	regexps := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("%s regex %q is invalid: %w", field, pattern, err)
		}
		regexps = append(regexps, compiled)
	}
	return regexps, nil
}

func (f metricFilter) match(name string) bool {
	if len(f.include) > 0 {
		included := false
		for _, re := range f.include {
			if re.MatchString(name) {
				included = true
				break
			}
		}
		if !included {
			return false
		}
	}
	for _, re := range f.exclude {
		if re.MatchString(name) {
			return false
		}
	}
	return true
}

func parsePrometheusLine(line string) (metricSample, bool) {
	expr, rawValue, ok := splitSampleLine(line)
	if !ok {
		return metricSample{}, false
	}
	value, err := strconv.ParseFloat(rawValue, 64)
	if err != nil {
		return metricSample{}, false
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return metricSample{}, false
	}
	name, labels, ok := parseMetricExpr(expr)
	if !ok || name == "" {
		return metricSample{}, false
	}
	return metricSample{Name: name, Labels: labels, Value: value}, true
}

func splitSampleLine(line string) (string, string, bool) {
	inQuote := false
	escaped := false
	for index, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if inQuote && r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && isASCIIWhitespace(r) {
			expr := strings.TrimSpace(line[:index])
			rest := strings.TrimSpace(line[index:])
			if expr == "" || rest == "" {
				return "", "", false
			}
			fields := strings.Fields(rest)
			if len(fields) == 0 || len(fields) > 2 {
				return "", "", false
			}
			if len(fields) == 2 {
				if _, err := strconv.ParseInt(fields[1], 10, 64); err != nil {
					return "", "", false
				}
			}
			return expr, fields[0], true
		}
	}
	return "", "", false
}

func parseMetricExpr(expr string) (string, map[string]string, bool) {
	open := strings.IndexByte(expr, '{')
	if open < 0 {
		if strings.Contains(expr, "}") {
			return "", nil, false
		}
		if !isPrometheusMetricName(expr) {
			return "", nil, false
		}
		return expr, map[string]string{}, true
	}
	if !strings.HasSuffix(expr, "}") {
		return "", nil, false
	}
	name := expr[:open]
	if !isPrometheusMetricName(name) {
		return "", nil, false
	}
	rawLabels := expr[open+1 : len(expr)-1]
	labels, ok := parseLabels(rawLabels)
	if !ok {
		return "", nil, false
	}
	return name, labels, true
}

func parseLabels(raw string) (map[string]string, bool) {
	labels := make(map[string]string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return labels, true
	}
	parts, ok := splitLabelPairs(raw)
	if !ok {
		return nil, false
	}
	for _, part := range parts {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, false
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !isPrometheusLabelKey(key) || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			return nil, false
		}
		if _, exists := labels[key]; exists {
			return nil, false
		}
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return nil, false
		}
		labels[key] = unquoted
	}
	return labels, true
}

func splitLabelPairs(raw string) ([]string, bool) {
	var pairs []string
	start := 0
	inQuote := false
	escaped := false
	for index, r := range raw {
		if escaped {
			escaped = false
			continue
		}
		if inQuote && r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && r == ',' {
			pair := strings.TrimSpace(raw[start:index])
			if pair == "" {
				return nil, false
			}
			pairs = append(pairs, pair)
			start = index + len(string(r))
		}
	}
	if inQuote || escaped {
		return nil, false
	}
	pair := strings.TrimSpace(raw[start:])
	if pair == "" {
		return nil, false
	}
	pairs = append(pairs, pair)
	return pairs, true
}

func isPrometheusMetricName(name string) bool {
	if name == "" {
		return false
	}
	for index, r := range name {
		if index == 0 {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || r == ':') {
				return false
			}
			continue
		}
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == ':') {
			return false
		}
	}
	return true
}

func isPrometheusLabelKey(key string) bool {
	if key == "" {
		return false
	}
	for index, r := range key {
		if index == 0 {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_') {
				return false
			}
			continue
		}
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

func isASCIIWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f'
}
