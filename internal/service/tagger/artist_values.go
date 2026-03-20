package tagger

import "strings"

var artistValueSeparators = []string{
	" / ",
	"; ",
	" feat. ",
	" ft. ",
	" featuring ",
	"、",
}

func splitArtistValues(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	values := []string{raw}
	for _, separator := range artistValueSeparators {
		next := make([]string, 0, len(values))
		split := false
		for _, value := range values {
			if !strings.Contains(value, separator) {
				next = append(next, value)
				continue
			}
			next = append(next, strings.Split(value, separator)...)
			split = true
		}
		if split {
			values = next
		}
	}

	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func encodeID3v24MultiValueText(raw string) string {
	values := splitArtistValues(raw)
	if len(values) == 0 {
		return strings.TrimSpace(raw)
	}
	if len(values) == 1 {
		return values[0]
	}
	return strings.Join(values, "\x00")
}

func appendVorbisMultiValueTags(args []string, key, raw string) []string {
	for _, value := range splitArtistValues(raw) {
		args = append(args, "--set-tag="+key+"="+value)
	}
	return args
}
