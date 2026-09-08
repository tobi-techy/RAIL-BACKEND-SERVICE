package simulation

import "testing"

func TestGradeConversation_Clean(t *testing.T) {
	d := gradeConversation("Yeah, that's the real issue. Want to start with the safety net?")
	if d.Score < 100 {
		t.Fatalf("expected a clean conversational reply to score 100, got %v (%v)", d.Score, d.Notes)
	}
}

func TestGradeConversation_PenalizesWallOfTextAndFillers(t *testing.T) {
	wall := "That makes sense. I understand. Great question. " + repeat("And so the money flows onward through the system, ", 30)
	d := gradeConversation(wall)
	if d.Score >= 100 {
		t.Fatalf("expected a wall-of-text filler reply to lose points, got %v", d.Score)
	}
}

func TestGradeConversation_PenalizesMultipleQuestions(t *testing.T) {
	d := gradeConversation("Do you want to save? Or invest? Or just get organized? What's the goal?")
	if d.Score >= 100 {
		t.Fatalf("expected multiple questions to lose points, got %v", d.Score)
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
