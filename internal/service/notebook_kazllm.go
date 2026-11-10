package service

import (
	"errors"
	"net"
	"strings"
	"unicode"

	"github.com/west6ide/workspace-backend/internal/alem"
)

// KazLLM's context window is not documented, so only a compact final prompt
// (~16k tokens of Cyrillic text plus room for the answer) is sent to it.
// Chunk analysis always uses the default model with its known large window.
const notebookKazPromptLimit = 48000

const notebookLanguageInstruction = " Отвечай на языке вопроса пользователя: на вопрос на казахском — на казахском, на русском — на русском. Цитаты из документов приводи на языке оригинала, без перевода."

const notebookKazakhLetters = "әғқңөұүһіӘҒҚҢӨҰҮҺІ"

// notebookIsKazakh reports a Kazakh question, an explicit kz interface
// language, or documents written mainly in Kazakh. Letters specific to the
// Kazakh alphabet do not occur in Russian text.
func notebookIsKazakh(question, lang string, texts []string) bool {
	if notebookPresetLang(lang) == "kz" || notebookKazakhCount(question, len(question)) >= 2 {
		return true
	}
	kazakh, cyrillic := 0, 0
	for _, text := range texts {
		n := 0
		for _, r := range text {
			if n++; n > 20000 {
				break
			}
			if unicode.Is(unicode.Cyrillic, r) {
				cyrillic++
				if strings.ContainsRune(notebookKazakhLetters, r) {
					kazakh++
				}
			}
		}
	}
	// Kazakh-specific letters make up roughly 5–10% of Kazakh text.
	return cyrillic >= 200 && kazakh*100 >= cyrillic*2
}

func notebookKazakhCount(text string, limit int) int {
	count, n := 0, 0
	for _, r := range text {
		if n++; n > limit {
			break
		}
		if strings.ContainsRune(notebookKazakhLetters, r) {
			count++
		}
	}
	return count
}

// notebookKazRetryable covers provider-side failures (context length, model
// errors, 5xx, transport timeouts) and empty answers. Accounting, limiter and
// cancellation errors are never retried on another model.
func notebookKazRetryable(err error, content string) bool {
	if err == nil {
		return content == ""
	}
	var apiErr *alem.APIError
	var netErr net.Error
	return errors.As(err, &apiErr) || errors.As(err, &netErr)
}
