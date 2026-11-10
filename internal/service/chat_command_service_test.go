package service

import "testing"

func TestChatCommandServiceResolveCommand(t *testing.T) {
	svc := &ChatCommandService{}

	tests := []struct {
		name        string
		commandID   string
		rawText     string
		wantCommand string
		wantRest    string
	}{
		{
			name:        "create task alias",
			rawText:     "/create_task Подготовить отчет",
			wantCommand: ChatCommandCreateKanbanTask,
			wantRest:    "Подготовить отчет",
		},
		{
			name:        "english board alias",
			rawText:     "/create_board Product Launch",
			wantCommand: ChatCommandCreateKanbanBoard,
			wantRest:    "Product Launch",
		},
		{
			name:        "list tasks alias",
			rawText:     "/list_tasks report",
			wantCommand: ChatCommandListKanbanTasks,
			wantRest:    "report",
		},
		{
			name:        "create event alias",
			rawText:     "/create_event Team sync",
			wantCommand: ChatCommandCreateEvent,
			wantRest:    "Team sync",
		},
		{
			name:        "today events alias",
			rawText:     "/today_events",
			wantCommand: ChatCommandTodayEvents,
			wantRest:    "",
		},
		{
			name:        "week events alias",
			rawText:     "/week_events",
			wantCommand: ChatCommandWeekEvents,
			wantRest:    "",
		},
		{
			name:        "find file alias",
			rawText:     "/find_file report",
			wantCommand: ChatCommandFindFile,
			wantRest:    "report",
		},
		{
			name:        "recent files alias",
			rawText:     "/recent_files",
			wantCommand: ChatCommandRecentFiles,
			wantRest:    "",
		},
		{
			name:        "share file alias",
			rawText:     "/share_file report.pdf",
			wantCommand: ChatCommandShareFile,
			wantRest:    "report.pdf",
		},
		{
			name:        "upload file alias",
			rawText:     "/upload_file",
			wantCommand: ChatCommandUploadFile,
			wantRest:    "",
		},
		{
			name:        "singular list task alias",
			rawText:     "/list_task",
			wantCommand: ChatCommandListKanbanTasks,
			wantRest:    "",
		},
		{
			name:        "explicit list tasks command id strips known alias",
			commandID:   ChatCommandListKanbanTasks,
			rawText:     "/list_tasks auth",
			wantCommand: ChatCommandListKanbanTasks,
			wantRest:    "auth",
		},
		{
			name:        "explicit command id strips known alias",
			commandID:   ChatCommandCreateKanbanTask,
			rawText:     "/create_task Fix auth",
			wantCommand: ChatCommandCreateKanbanTask,
			wantRest:    "Fix auth",
		},
		{
			name:        "unknown command",
			rawText:     "/unknown data",
			wantCommand: "",
			wantRest:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCommand, gotRest := svc.resolveCommand(tt.commandID, tt.rawText)
			if gotCommand != tt.wantCommand {
				t.Fatalf("command = %q, want %q", gotCommand, tt.wantCommand)
			}
			if gotRest != tt.wantRest {
				t.Fatalf("rest = %q, want %q", gotRest, tt.wantRest)
			}
		})
	}
}
