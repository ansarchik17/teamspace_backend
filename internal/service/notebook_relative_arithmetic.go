package service

import (
	"math/big"
	"regexp"
	"strings"
	"unicode"
)

const notebookArithmeticSpace = `[\s\x{00a0}\x{202f}]`

var notebookMultiplierClaim = regexp.MustCompile(`(?i)(^|[^\p{L}])(удвоил(?:ся|ась|ось|ись)|утроил(?:ся|ась|ось|ись))` + notebookArithmeticSpace + `*\(` + notebookArithmeticSpace + `*с` + notebookArithmeticSpace + `+([0-9][0-9\s\x{00a0}\x{202f}]*)` + `(?:[\p{L}\p{Sc}]+` + notebookArithmeticSpace + `*)?до` + notebookArithmeticSpace + `+([0-9][0-9\s\x{00a0}\x{202f}]*)` + `(?:[\p{L}\p{Sc}]+` + notebookArithmeticSpace + `*)?\)`)
var notebookSourceInteger = regexp.MustCompile(`([0-9]+(?:[ \x{00a0}\x{202f}][0-9]{3})*)(?:$|[^0-9])`)

func notebookArithmeticInteger(value string) (*big.Int, bool) {
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
	return new(big.Int).SetString(value, 10)
}

// Only repair explicit, unquoted multiplier claims whose two values occur in
// current selected sources. Never infer a value, alter quotations or change
// unspecified comparisons. Integer arithmetic avoids overflow/rounding.
func correctNotebookRelativeArithmetic(answer string, sources []NotebookResearchSource) string {
	known := map[string]bool{}
	for _, source := range sources {
		if source.Status != "ready" {
			continue
		}
		for _, excerpt := range source.Excerpts {
			for _, value := range notebookSourceInteger.FindAllStringSubmatch(excerpt.Text, -1) {
				if n, ok := notebookArithmeticInteger(value[1]); ok {
					known[n.String()] = true
				}
			}
		}
	}
	lines := strings.Split(answer, "\n")
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			mark := trimmed[:3]
			if fence == "" {
				fence = mark
			} else if fence == mark {
				fence = ""
			}
			continue
		}
		if fence != "" || strings.HasPrefix(trimmed, ">") || strings.ContainsAny(line, "`\"'«»“”") {
			continue
		}
		plain := strings.ToLower(strings.Join(strings.Fields(line), " "))
		if strings.Contains(plain, "не удво") || strings.Contains(plain, "не утро") {
			continue
		}
		lines[i] = notebookMultiplierClaim.ReplaceAllStringFunc(line, func(claim string) string {
			m := notebookMultiplierClaim.FindStringSubmatch(claim)
			before, okA := notebookArithmeticInteger(m[3])
			after, okB := notebookArithmeticInteger(m[4])
			if !okA || !okB || before.Sign() <= 0 || !known[before.String()] || !known[after.String()] {
				return claim
			}
			factor := int64(2)
			if strings.HasPrefix(strings.ToLower(m[2]), "утро") {
				factor = 3
			}
			if new(big.Int).Mul(before, big.NewInt(factor)).Cmp(after) == 0 {
				return claim
			}
			verb := "увеличил"
			if after.Cmp(before) < 0 {
				verb = "уменьшил"
			}
			if after.Cmp(before) == 0 {
				verb = "не изменил"
			}
			for _, ending := range []string{"ся", "ась", "ось", "ись"} {
				if strings.HasSuffix(strings.ToLower(m[2]), ending) {
					verb += ending
					break
				}
			}
			if []rune(m[2])[0] == unicode.ToUpper([]rune(m[2])[0]) {
				r := []rune(verb)
				r[0] = unicode.ToUpper(r[0])
				verb = string(r)
			}
			return m[1] + verb + strings.TrimPrefix(claim, m[1]+m[2])
		})
	}
	return strings.Join(lines, "\n")
}
