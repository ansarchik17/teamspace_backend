package service

import (
	"strconv"
	"strings"

	"github.com/west6ide/workspace-backend/internal/apperrors"
)

// NotebookStudioPreset is a ready-made Studio prompt. The client shows the
// catalog as cards; Ask expands the chosen preset into the question and format.
type NotebookStudioPreset struct {
	ID          string               `json:"id"`
	Format      string               `json:"format"`
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Prompt      string               `json:"prompt"`
	MinSources  int                  `json:"min_sources"`
	Count       *NotebookPresetCount `json:"count,omitempty"`
}

type NotebookPresetCount struct {
	Default int `json:"default"`
	Max     int `json:"max"`
}

type notebookPresetText struct{ title, description, prompt string }

type notebookPresetDef struct {
	id         string
	format     string
	minSources int
	count      *NotebookPresetCount
	text       map[string]notebookPresetText
}

// Prompts use {{count}} and {{focus}}. Focus is the user's own topic and is
// appended as a separate sentence, never interpreted as a format instruction.
var notebookPresetDefs = []notebookPresetDef{
	{id: "report", format: "report", minSources: 1, text: map[string]notebookPresetText{
		"ru": {"Отчёт", "Аналитический отчёт: цель, ключевые факты, выводы", "Составь аналитический отчёт по выбранным документам: цель, ключевые факты и цифры, выводы и рекомендации."},
		"kz": {"Есеп", "Аналитикалық есеп: мақсат, негізгі деректер, қорытынды", "Таңдалған құжаттар бойынша аналитикалық есеп құрастыр: мақсаты, негізгі деректер мен сандар, қорытындылар мен ұсыныстар."},
	}},
	{id: "presentation", format: "presentation", minSources: 1, count: &NotebookPresetCount{Default: 5, Max: 15}, text: map[string]notebookPresetText{
		"ru": {"Презентация", "Слайды с тезисами по документам", "Подготовь презентацию из {{count}} слайдов по выбранным документам."},
		"kz": {"Презентация", "Құжаттар бойынша тезистері бар слайдтар", "Таңдалған құжаттар бойынша {{count}} слайдтан тұратын презентация дайында."},
	}},
	{id: "table", format: "table", minSources: 1, text: map[string]notebookPresetText{
		"ru": {"Таблица", "Ключевые показатели, даты и ответственные в таблице", "Сведи ключевые сущности, показатели, даты и ответственных из документов в таблицу."},
		"kz": {"Кесте", "Негізгі көрсеткіштер, күндер және жауаптылар кестеде", "Құжаттардағы негізгі нысандарды, көрсеткіштерді, күндерді және жауаптыларды кестеге жинақта."},
	}},
	{id: "chart", format: "chart", minSources: 1, text: map[string]notebookPresetText{
		"ru": {"График", "Диаграмма по числовым данным документов", "Построй график по числовым данным из документов."},
		"kz": {"График", "Құжаттардағы сандық деректер бойынша диаграмма", "Құжаттардағы сандық деректер бойынша график құр."},
	}},
	{id: "cards", format: "cards", minSources: 1, count: &NotebookPresetCount{Default: 10, Max: 30}, text: map[string]notebookPresetText{
		"ru": {"Карточки", "Карточки «вопрос — ответ» для запоминания", "Создай {{count}} карточек «вопрос — ответ» по ключевым фактам документов."},
		"kz": {"Карточкалар", "Есте сақтауға арналған «сұрақ — жауап» карточкалары", "Құжаттардың негізгі деректері бойынша {{count}} «сұрақ — жауап» карточкасын жаса."},
	}},
	{id: "test", format: "test", minSources: 1, count: &NotebookPresetCount{Default: 5, Max: 30}, text: map[string]notebookPresetText{
		"ru": {"Тест", "Тест с вариантами ответа для проверки знаний", "Составь тест из {{count}} вопросов с четырьмя вариантами ответа по документам."},
		"kz": {"Тест", "Білімді тексеруге арналған жауап нұсқалары бар тест", "Құжаттар бойынша төрт жауап нұсқасы бар {{count}} сұрақтан тұратын тест құрастыр."},
	}},
	{id: "mermaid", format: "mermaid", minSources: 1, text: map[string]notebookPresetText{
		"ru": {"Диаграмма", "Схема процессов, этапов или связей", "Построй диаграмму процессов, этапов или связей, описанных в документах."},
		"kz": {"Сызба", "Процестер, кезеңдер немесе байланыстар сызбасы", "Құжаттарда сипатталған процестердің, кезеңдердің немесе байланыстардың сызбасын құр."},
	}},
	{id: "compare", format: "compare", minSources: 2, text: map[string]notebookPresetText{
		"ru": {"Сравнение", "Что совпадает и чем отличаются документы", "Сравни выбранные документы: что совпадает, что отличается и что есть только в одном из них."},
		"kz": {"Салыстыру", "Құжаттардың ортақ және айырмашылық тұстары", "Таңдалған құжаттарды салыстыр: не сәйкес келеді, не ерекшеленеді және не тек біреуінде ғана бар."},
	}},
	{id: "summary", format: "text", minSources: 1, text: map[string]notebookPresetText{
		"ru": {"Краткое резюме", "О чём документы и главные факты", "Сделай краткое резюме документов: о чём они, 5–7 главных фактов с цифрами и датами и итог."},
		"kz": {"Қысқаша түйін", "Құжаттар не туралы және басты деректер", "Құжаттардың қысқаша түйінін жаса: олар не туралы, сандар мен күндері бар 5–7 басты дерек және қорытынды."},
	}},
	{id: "risks", format: "report", minSources: 1, text: map[string]notebookPresetText{
		"ru": {"Риски", "Риски, ограничения и противоречия", "Выдели риски, ограничения, противоречия и спорные места в документах; для каждого укажи возможные последствия и рекомендацию."},
		"kz": {"Тәуекелдер", "Тәуекелдер, шектеулер және қайшылықтар", "Құжаттардағы тәуекелдерді, шектеулерді, қайшылықтарды және даулы тұстарды анықта; әрқайсысы үшін ықтимал салдары мен ұсынысты көрсет."},
	}},
	{id: "timeline", format: "table", minSources: 1, text: map[string]notebookPresetText{
		"ru": {"Хронология", "События, сроки и дедлайны по датам", "Составь хронологию событий, сроков и дедлайнов из документов в виде таблицы со столбцами: дата, событие, ответственный, источник."},
		"kz": {"Хронология", "Оқиғалар, мерзімдер және дедлайндар күн бойынша", "Құжаттардағы оқиғалардың, мерзімдердің және дедлайндардың хронологиясын кесте түрінде құрастыр: күні, оқиға, жауапты, дереккөз."},
	}},
	{id: "checklist", format: "text", minSources: 1, text: map[string]notebookPresetText{
		"ru": {"Чек-лист", "Конкретные действия и требования", "Составь чек-лист конкретных действий и требований из документов с ответственными и сроками, если они указаны."},
		"kz": {"Чек-парақ", "Нақты әрекеттер мен талаптар", "Құжаттардағы нақты әрекеттер мен талаптардың чек-парағын жаса, егер көрсетілсе, жауаптылары мен мерзімдерін қос."},
	}},
	{id: "faq", format: "text", minSources: 1, count: &NotebookPresetCount{Default: 8, Max: 20}, text: map[string]notebookPresetText{
		"ru": {"FAQ", "Частые вопросы и ответы по документам", "Составь {{count}} частых вопросов с ответами по документам."},
		"kz": {"FAQ", "Құжаттар бойынша жиі қойылатын сұрақтар", "Құжаттар бойынша жауаптарымен бірге жиі қойылатын {{count}} сұрақ құрастыр."},
	}},
}

func notebookPresetLang(lang string) string {
	if strings.EqualFold(strings.TrimSpace(lang), "kz") || strings.EqualFold(strings.TrimSpace(lang), "kk") {
		return "kz"
	}
	return "ru"
}

func NotebookStudioPresets(lang string) []NotebookStudioPreset {
	lang = notebookPresetLang(lang)
	presets := make([]NotebookStudioPreset, 0, len(notebookPresetDefs))
	for _, d := range notebookPresetDefs {
		t := d.text[lang]
		presets = append(presets, NotebookStudioPreset{ID: d.id, Format: d.format, Title: t.title, Description: t.description, Prompt: t.prompt, MinSources: d.minSources, Count: d.count})
	}
	return presets
}

// expandNotebookPreset returns the question and format for a preset request.
// The user's typed question, if any, becomes the focus of the preset.
func expandNotebookPreset(req NotebookResearchRequest) (question, format, title string, err error) {
	lang := notebookPresetLang(req.Lang)
	for _, d := range notebookPresetDefs {
		if d.id != req.Preset {
			continue
		}
		t := d.text[lang]
		prompt := t.prompt
		if d.count != nil {
			count := req.Count
			if count <= 0 {
				count = d.count.Default
			}
			count = min(count, d.count.Max)
			prompt = strings.ReplaceAll(prompt, "{{count}}", strconv.Itoa(count))
		}
		focus := strings.TrimSpace(req.Focus)
		if focus == "" {
			focus = strings.TrimSpace(req.Question)
		}
		if focus != "" {
			if lang == "kz" {
				prompt += " Ерекше назар аудар: " + focus
			} else {
				prompt += " Особый фокус: " + focus
			}
		}
		// An empty selection is answered with status no_sources, not an error.
		if d.minSources > 1 && len(req.FileIDs) < d.minSources {
			return "", "", "", apperrors.Validation("Для этого формата выберите минимум " + strconv.Itoa(d.minSources) + " источника")
		}
		return prompt, d.format, t.title, nil
	}
	return "", "", "", apperrors.Validation("unsupported studio preset")
}
