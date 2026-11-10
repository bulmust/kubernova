package model

import "strings"

// ImageRef is a parsed container image reference.
//
// Digests are never included in any field. A reference without a tag
// (including a digest-only reference) uses "-" for Tag so the table cell is
// never blank; that choice is also documented in the README. The repository
// never includes the registry host.
type ImageRef struct {
	Registry   string
	Repository string
	Tag        string
}

// ParseImage parses registry, repository, and tag without contacting a
// registry.
func ParseImage(reference string) ImageRef {
	reference = strings.TrimSpace(reference)
	if reference == "" || reference == "-" {
		return ImageRef{Registry: "-", Repository: "-", Tag: "-"}
	}

	// A digest is not a tag and is removed from all parsed fields.
	if index := strings.Index(reference, "@"); index >= 0 {
		reference = reference[:index]
	}

	registry := "docker.io"
	remainder := reference
	if firstSlash := strings.Index(reference, "/"); firstSlash >= 0 {
		first := reference[:firstSlash]
		if strings.ContainsAny(first, ".:") || strings.EqualFold(first, "localhost") {
			registry = first
			remainder = reference[firstSlash+1:]
		}
	}

	repository := remainder
	tag := "-"
	if lastSlash := strings.LastIndex(remainder, "/"); lastSlash >= 0 {
		if colon := strings.LastIndex(remainder[lastSlash+1:], ":"); colon >= 0 {
			colon += lastSlash + 1
			repository = remainder[:colon]
			tag = remainder[colon+1:]
		}
	} else if colon := strings.LastIndex(remainder, ":"); colon >= 0 {
		// Without a slash the last colon separates a tag; a registry port is
		// only meaningful before the repository path.
		repository = remainder[:colon]
		tag = remainder[colon+1:]
	}

	if repository == "" {
		repository = "-"
	}
	if tag == "" {
		tag = "-"
	}
	if registry == "" {
		registry = "-"
	}
	return ImageRef{Registry: registry, Repository: repository, Tag: tag}
}
