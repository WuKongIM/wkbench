package metrics_collector

import (
	"fmt"
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
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return metricSample{}, false
	}
	value, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return metricSample{}, false
	}
	name, labels, ok := parseMetricExpr(fields[0])
	if !ok || name == "" {
		return metricSample{}, false
	}
	return metricSample{Name: name, Labels: labels, Value: value}, true
}

func parseMetricExpr(expr string) (string, map[string]string, bool) {
	open := strings.IndexByte(expr, '{')
	if open < 0 {
		if strings.Contains(expr, "}") {
			return "", nil, false
		}
		return expr, map[string]string{}, true
	}
	if !strings.HasSuffix(expr, "}") {
		return "", nil, false
	}
	name := expr[:open]
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
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, false
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !isPrometheusLabelKey(key) || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			return nil, false
		}
		labels[key] = strings.Trim(value[1:len(value)-1], " ")
	}
	return labels, true
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
