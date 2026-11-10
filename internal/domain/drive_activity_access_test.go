package domain

import "testing"

func TestPositionGrantsFullDriveActivity(t *testing.T) {
	tests := []struct {
		name     string
		position string
		want     bool
	}{
		{name: "minister", position: "Министр транспорта", want: true},
		{name: "vice minister", position: "Вице-министр", want: true},
		{name: "head of staff", position: "Руководитель аппарата", want: true},
		{name: "ordinary specialist", position: "Главный эксперт", want: false},
		{name: "blank", position: "   ", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PositionGrantsFullDriveActivity(tt.position); got != tt.want {
				t.Fatalf("PositionGrantsFullDriveActivity(%q) = %v, want %v", tt.position, got, tt.want)
			}
		})
	}
}
