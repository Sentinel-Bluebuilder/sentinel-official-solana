package main

import (
	"strings"
	"unicode"

	"github.com/gagliardetto/anchor-go/tools"
)

var initialisms = map[string]string{
	"id":     "ID",
	"uri":    "URI",
	"url":    "URL",
	"gb":     "GB",
	"ttl":    "TTL",
	"ata":    "ATA",
	"pda":    "PDA",
	"rpc":    "RPC",
	"api":    "API",
	"json":   "JSON",
	"ws":     "WS",
	"tls":    "TLS",
	"sha256": "SHA256",
	"sdk":    "SDK",
	"pubkey": "PublicKey",
}

func batchName(name string) string {
	return "Fetch" + name + "Batch"
}

func exportedName(s string) string {
	return strings.Join(nameWords(s), "")
}

// nameWords splits snake_case, SCREAMING_SNAKE and CamelCase names into words,
// replacing the initialisms with their Go spelling and capitalizing the rest.
func nameWords(s string) []string {
	var words []string

	for _, segment := range strings.Split(s, "_") {
		if segment == "" {
			continue
		}

		if segment == strings.ToUpper(segment) && strings.ContainsFunc(segment, unicode.IsLetter) {
			segment = strings.ToLower(segment)
		}

		for _, word := range splitCamel(segment) {
			if initialism, ok := initialisms[strings.ToLower(word)]; ok {
				words = append(words, initialism)

				continue
			}

			words = append(words, strings.ToUpper(word[:1])+word[1:])
		}
	}

	return words
}

// splitCamel splits on lower-to-upper transitions and before the last capital
// of an upper-case run that precedes a lower-case letter. Digits stay with the
// word they follow.
func splitCamel(s string) []string {
	runes := []rune(s)
	start := 0

	var words []string

	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := unicode.IsUpper(cur) && (unicode.IsLower(prev) || unicode.IsDigit(prev))

		if unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
			boundary = true
		}

		if boundary {
			words = append(words, string(runes[start:i]))
			start = i
		}
	}

	return append(words, string(runes[start:]))
}

func unexportedName(s string) string {
	words := nameWords(s)
	if len(words) == 0 {
		return ""
	}

	words[0] = strings.ToLower(words[0][:1]) + words[0][1:]
	if _, ok := initialisms[strings.ToLower(words[0])]; ok {
		words[0] = strings.ToLower(words[0])
	}

	name := strings.Join(words, "")
	if tools.IsReservedKeyword(name) {
		return name + "_"
	}

	return name
}
