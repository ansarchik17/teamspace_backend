package domain

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Keep the naming rule in sync with Workspace's companyMailAddress.ts.
var employeeLetters = map[rune]string{
	'а': "a", 'ә': "a", 'б': "b", 'в': "v", 'г': "g", 'ғ': "g", 'д': "d", 'е': "e", 'ё': "yo", 'ж': "zh",
	'з': "z", 'и': "i", 'й': "i", 'к': "k", 'қ': "q", 'л': "l", 'м': "m", 'н': "n", 'ң': "ng", 'о': "o",
	'ө': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ұ': "u", 'ү': "u", 'ф': "f", 'х': "kh",
	'һ': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch", 'ы': "y", 'і': "i", 'э': "e", 'ю': "yu", 'я': "ya", 'ъ': "", 'ь': "",
}
var mailLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func CompanyMailDomain(slug, base string) string {
	slug, base = strings.ToLower(strings.TrimSpace(slug)), strings.ToLower(strings.TrimSpace(base))
	if !mailLabel.MatchString(slug) || !strings.Contains(base, ".") || len(slug)+len(base)+1 > 253 {
		return ""
	}
	for _, label := range strings.Split(base, ".") {
		if !mailLabel.MatchString(label) {
			return ""
		}
	}
	return slug + "." + base
}

func SuggestedEmployeeLogin(firstName, lastName string) string {
	latin := func(value string) string {
		var mapped strings.Builder
		for _, ch := range strings.ToLower(strings.TrimSpace(value)) {
			if replacement, ok := employeeLetters[ch]; ok {
				mapped.WriteString(replacement)
			} else {
				mapped.WriteRune(ch)
			}
		}
		var result strings.Builder
		separator := false
		for _, ch := range norm.NFD.String(mapped.String()) {
			if unicode.Is(unicode.Mn, ch) {
				continue
			}
			if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
				result.WriteRune(ch)
				separator = false
			} else if !separator {
				result.WriteByte('-')
				separator = true
			}
		}
		return strings.Trim(result.String(), "-")
	}
	first, last := latin(firstName), latin(lastName)
	if first == "" || last == "" {
		return ""
	}
	login := first[:1] + "." + last
	if len(login) > 50 {
		login = login[:50]
	}
	return strings.TrimRight(login, ".-")
}
