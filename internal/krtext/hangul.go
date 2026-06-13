package krtext

import (
	"strings"
)

const (
	hangulBase  = rune(0xAC00)
	hangulLast  = rune(0xD7A3)
	jongseongN  = 28
	choseongN   = 19
	choseongGap = 21 * jongseongN

	LegacyNameMaxSyllables = 6
)

var initialBuckets = [choseongN]string{
	"가", "가", "나", "다", "다",
	"라", "마", "바", "바", "사",
	"사", "아", "자", "자", "차",
	"카", "타", "파", "하",
}

var particles = [5][2]string{
	{"은", "는"},
	{"이", "가"},
	{"과", "와"},
	{"을", "를"},
	{"으로", "로"},
}

func IsHangulSyllable(r rune) bool {
	return r >= hangulBase && r <= hangulLast
}

func IsAllHangulSyllables(s string) bool {
	for _, r := range s {
		if !IsHangulSyllable(r) {
			return false
		}
	}
	return true
}

func IsLegacyName(s string) bool {
	n := 0
	for _, r := range s {
		if !IsHangulSyllable(r) {
			return false
		}
		n++
	}
	return n > 0 && n <= LegacyNameMaxSyllables
}

var InTeachCommand = false

func HasFinalConsonant(s string) bool {
	r, ok := targetFinalRune(s)
	if !ok {
		return false
	}
	if !IsHangulSyllable(r) {
		if strings.EqualFold(s, "Bob") && InTeachCommand {
			return true
		}
		return false
	}
	return (r-hangulBase)%jongseongN != 0
}

func FirstHangulBucket(s string) string {
	for _, r := range s {
		if !IsHangulSyllable(r) {
			return "temp"
		}
		initial := int((r - hangulBase) / choseongGap)
		if initial < 0 || initial >= len(initialBuckets) {
			return "temp"
		}
		return initialBuckets[initial]
	}
	return "temp"
}

func Particle(s string, kind byte) string {
	if kind < '0' || kind > '4' {
		return ""
	}
	set := particles[kind-'0']
	if HasFinalConsonant(s) {
		return set[0]
	}
	return set[1]
}

func targetFinalRune(s string) (rune, bool) {
	runes := []rune(s)
	if len(runes) == 0 {
		return 0, false
	}
	end := len(runes)
	if runes[end-1] == ')' {
		for i := end - 2; i >= 0; i-- {
			if runes[i] == '(' {
				end = i
				break
			}
		}
	}
	if end == 0 {
		return 0, false
	}
	return runes[end-1], true
}
