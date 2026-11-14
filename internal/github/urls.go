package github

import (
	"regexp"
	"strings"
)

func PRTag(url string) string {
	m := URLPattern.FindStringSubmatch(url)
	if m == nil {
		return "PR"
	}
	word := m[2]
	if i := strings.LastIndex(word, "-"); i >= 0 && i < len(word)-1 {
		word = word[i+1:]
	}
	return strings.ToUpper(word[:1]) + "#" + m[3]
}

func PRLabel(url string) string {
	if m := URLPattern.FindStringSubmatch(url); m != nil {
		return m[2] + " #" + m[3]
	}
	return url
}

func CanonicalPR(url string) string {
	if m := URLPattern.FindStringSubmatch(url); m != nil {
		return "https://github.com/" + m[1] + "/" + m[2] + "/pull/" + m[3]
	}
	return url
}

var URLPattern = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/pull/(\d+)`)
