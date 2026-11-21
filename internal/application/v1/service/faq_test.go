package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// knowledge is the knowledge base over memory, with somebody who writes it and
// somebody who only reads it.
type knowledge struct {
	faq    *service.FAQApplicationService
	writer *service.Principal
	reader *service.Principal
}

func newKnowledge(t *testing.T) knowledge {
	t.Helper()

	users := memory.NewUserRepository()
	wendy, err := users.Save(context.Background(), &model.User{Name: "Wendy Writer", Email: "wendy@example.com"})
	if err != nil {
		t.Fatal(err)
	}

	stored := memory.NewFAQRepository()

	return knowledge{
		faq:    service.NewFAQApplicationService(stored.Categories(), stored.Articles(), users),
		writer: &service.Principal{User: wendy, Permissions: []string{model.PermFAQRead, model.PermFAQWrite}},
		reader: &service.Principal{User: &model.User{ID: 99, Name: "Rita Reader"}, Permissions: []string{model.PermFAQRead}},
	}
}

func (k knowledge) category(t *testing.T, name string, parent *uint) *model.FAQCategory {
	t.Helper()

	made, err := k.faq.CreateCategory(context.Background(), command.FAQCategoryCommand{Name: ptr(name), ParentID: parent})
	if err != nil {
		t.Fatalf("make the category %s: %v", name, err)
	}

	return made
}

func (k knowledge) article(t *testing.T, cmd command.FAQArticleCommand) *model.FAQArticle {
	t.Helper()

	made, err := k.faq.CreateArticle(context.Background(), k.writer, cmd)
	if err != nil {
		t.Fatalf("write the article %s: %v", *cmd.Title, err)
	}

	return made
}

// The categories are a tree that cannot hold itself: a category is refused a
// place under itself or under one below it, and one that holds articles or
// other categories is not deleted, the refusal saying how many.
func TestTheKnowledgeBaseIsFiledInATreeOfCategories(t *testing.T) {
	ctx := context.Background()
	k := newKnowledge(t)
	printers := k.category(t, "Printers", nil)
	laser := k.category(t, "Laser", &printers.ID)

	_, err := k.faq.UpdateCategory(ctx, printers.ID, command.FAQCategoryCommand{ParentID: &laser.ID})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "faqCategoryUnderItself" {
		t.Errorf("putting Printers under Laser, which sits under it, was answered %v", err)
	}

	nowhere := uint(99)
	if _, err := k.faq.CreateCategory(ctx, command.FAQCategoryCommand{Name: ptr("Ink"), ParentID: &nowhere}); err == nil {
		t.Error("a category under one that is not there was kept")
	}

	if _, err := k.faq.CreateCategory(ctx, command.FAQCategoryCommand{Name: ptr("Laser")}); err == nil {
		t.Error("a second category named Laser was kept")
	}

	err = k.faq.DeleteCategory(ctx, printers.ID)
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "faqCategoryHasChildren" {
		t.Errorf("deleting Printers, with Laser under it, was answered %v", err)
	}

	k.article(t, command.FAQArticleCommand{CategoryID: &laser.ID, Title: ptr("Toner"), Body: ptr("Shake it.")})

	err = k.faq.DeleteCategory(ctx, laser.ID)
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "faqCategoryInUse" || detail.Values[1] != 1 {
		t.Errorf("deleting Laser, with an article in it, was answered %v", err)
	}

	moved, err := k.faq.UpdateCategory(ctx, laser.ID, command.FAQCategoryCommand{ClearParent: true})
	if err != nil || moved.ParentID != nil {
		t.Errorf("putting Laser at the top left it as %+v, %v", moved, err)
	}
}

// An article is written for the staff alone unless told otherwise, signed by
// whoever wrote it, and found by any of its words - in the title, the keywords
// or the text, whatever their case - and by its category. A retired one is
// hidden from whoever only reads the knowledge base and shown to its writers.
func TestAnArticleIsWrittenAndFound(t *testing.T) {
	ctx := context.Background()
	k := newKnowledge(t)
	printers := k.category(t, "Printers", nil)
	network := k.category(t, "Network", nil)

	toner := k.article(t, command.FAQArticleCommand{CategoryID: &printers.ID, Title: ptr(" Toner "),
		Body: ptr("Shake the cartridge."), Keywords: ptr("smudge")})
	k.article(t, command.FAQArticleCommand{CategoryID: &network.ID, Title: ptr("VPN"), Body: ptr("Reconnect.")})

	if toner.Title != "Toner" || toner.Visibility != model.VisibilityInternal || !toner.Valid ||
		toner.UpdatedBy == nil || *toner.UpdatedBy != k.writer.User.ID {
		t.Fatalf("the article was kept as %+v", toner)
	}

	for words, want := range map[string]int{"TONER": 1, "Smudge": 1, "cartridge": 1, "": 2, "nothing": 0} {
		if found, err := k.faq.Search(ctx, k.reader, words, 0); err != nil || len(found) != want {
			t.Errorf("searching for %q found %d, %v; want %d", words, len(found), err, want)
		}
	}

	if found, _ := k.faq.Search(ctx, k.reader, "", network.ID); len(found) != 1 || found[0].Title != "VPN" {
		t.Errorf("the network's category holds %v", found)
	}

	if _, err := k.faq.UpdateArticle(ctx, k.writer, toner.ID, command.FAQArticleCommand{Valid: ptr(false)}); err != nil {
		t.Fatal(err)
	}

	if found, _ := k.faq.Search(ctx, k.reader, "toner", 0); len(found) != 0 {
		t.Errorf("a reader finds the retired article: %v", found)
	}

	if _, err := k.faq.Article(ctx, k.reader, toner.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("a reader opening the retired article was answered %v", err)
	}

	if found, _ := k.faq.Search(ctx, k.writer, "toner", 0); len(found) != 1 {
		t.Errorf("a writer does not find the retired article: %v", found)
	}
}

// An article that could not be kept is refused with every field at fault: no
// category, title or text; a visibility or a language there is not; keywords
// too long; a retired category named anew.
func TestAnArticleThatCouldNotBeKeptIsRefused(t *testing.T) {
	ctx := context.Background()
	k := newKnowledge(t)
	printers := k.category(t, "Printers", nil)
	old, _ := k.faq.CreateCategory(ctx, command.FAQCategoryCommand{Name: ptr("Old"), Valid: ptr(false)})

	sent := func(change func(*command.FAQArticleCommand)) command.FAQArticleCommand {
		cmd := command.FAQArticleCommand{CategoryID: &printers.ID, Title: ptr("Toner"), Body: ptr("Shake it.")}
		change(&cmd)

		return cmd
	}

	for name, refused := range map[string]struct {
		sent   command.FAQArticleCommand
		fields []string
	}{
		"nothing":                 {command.FAQArticleCommand{}, []string{"categoryId", "title", "body"}},
		"a visibility there isnt": {sent(func(c *command.FAQArticleCommand) { c.Visibility = ptr("everybody") }), []string{"visibility"}},
		"a language not spoken":   {sent(func(c *command.FAQArticleCommand) { c.Language = ptr("fr") }), []string{"language"}},
		"keywords too long":       {sent(func(c *command.FAQArticleCommand) { c.Keywords = ptr(strings.Repeat("k", model.MaxNameLength+1)) }), []string{"keywords"}},
		"a retired category":      {sent(func(c *command.FAQArticleCommand) { c.CategoryID = &old.ID }), []string{"categoryId"}},
		"an empty text":           {sent(func(c *command.FAQArticleCommand) { c.Body = ptr(" ") }), []string{"body"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := k.faq.CreateArticle(ctx, k.writer, refused.sent)

			detail, ok := apperror.Detail(err)
			if !ok || strings.Join(detail.Fields, ",") != strings.Join(refused.fields, ",") {
				t.Errorf("the article was answered %v; want %v refused", err, refused.fields)
			}
		})
	}
}

// An article changed is signed by whoever changed it, when they did, and keeps
// when it was first written.
func TestAnArticleChangedIsSignedAgain(t *testing.T) {
	ctx := context.Background()
	k := newKnowledge(t)
	clock := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	k.faq.WithClock(func() time.Time { return clock })

	printers := k.category(t, "Printers", nil)
	toner := k.article(t, command.FAQArticleCommand{CategoryID: &printers.ID, Title: ptr("Toner"), Body: ptr("Shake it.")})

	clock = clock.Add(time.Hour)

	changed, err := k.faq.UpdateArticle(ctx, k.writer, toner.ID, command.FAQArticleCommand{
		Visibility: ptr("public"), Language: ptr("en"),
	})
	if err != nil || changed.Visibility != model.VisibilityPublic || changed.Language != "en" ||
		!changed.UpdatedAt.After(toner.UpdatedAt) || changed.CreatedAt != toner.CreatedAt {
		t.Errorf("the changed article reads %+v, %v", changed, err)
	}
}
