package model_test

import (
	"testing"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// The time between answers is counted as OTRS counts it: from the agent's last
// answer the customer saw, or - once the customer has written since - from
// the first of what they wrote; a note nobody outside saw and what the
// application wrote count for neither, and with neither it counts from the
// opening.
func TestTheTimeBetweenAnswersIsCountedFromTheLastContact(t *testing.T) {
	opened := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return opened.Add(time.Duration(minutes) * time.Minute) }

	agent := func(minutes int, visible bool) *model.Article {
		return &model.Article{Sender: model.SenderAgent, CustomerVisible: visible, CreatedAt: at(minutes)}
	}
	customer := func(minutes int) *model.Article {
		return &model.Article{Sender: model.SenderCustomer, CustomerVisible: true, CreatedAt: at(minutes)}
	}
	system := func(minutes int) *model.Article {
		return &model.Article{Sender: model.SenderSystem, CustomerVisible: true, CreatedAt: at(minutes)}
	}

	for _, c := range []struct {
		name     string
		articles []*model.Article
		want     time.Time
	}{
		{"nothing written", nil, opened},
		{"an internal note alone", []*model.Article{agent(0, false)}, opened},
		{"the agent answered last", []*model.Article{customer(0), agent(30, true)}, at(30)},
		{"the customer wrote after the answer", []*model.Article{customer(0), agent(30, true), customer(60)}, at(60)},
		{"the customer wrote twice since", []*model.Article{agent(10, true), customer(60), customer(90)}, at(60)},
		{"a note after the customer", []*model.Article{agent(10, true), customer(60), agent(70, false)}, at(60)},
		{"the application wrote last", []*model.Article{agent(10, true), system(20)}, at(10)},
		{"the customer opened it", []*model.Article{customer(0)}, opened},
	} {
		if got := model.UpdateFrom(c.articles, opened); !got.Equal(c.want) {
			t.Errorf("%s: counted from %v, want %v", c.name, got, c.want)
		}
	}
}
