package service_test

import (
	"context"
	"slices"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// What is published of the knowledge base depends on who reads it from outside
// the staff: a customer reads what is meant for customers and what is public,
// anybody what is public alone - and nobody outside the staff an internal
// article or a retired one, found or opened. The categories named are those
// holding something the reader may read, and the ones above them.
func TestTheKnowledgeBaseIsPublishedToWhoeverMayReadIt(t *testing.T) {
	ctx := context.Background()
	k := newKnowledge(t)

	hardware := k.category(t, "Hardware", nil)
	printers := k.category(t, "Printers", &hardware.ID)
	servers := k.category(t, "Servers", &hardware.ID)
	k.category(t, "Empty", nil)

	write := func(title string, category *model.FAQCategory, visibility model.FAQVisibility) *model.FAQArticle {
		return k.article(t, command.FAQArticleCommand{
			Title: ptr(title), Body: ptr("How to."), CategoryID: &category.ID, Visibility: ptr(string(visibility)),
		})
	}

	public := write("Paper jam", printers, model.VisibilityPublic)
	external := write("Toner swap", printers, model.VisibilityExternal)
	internal := write("Root password", servers, model.VisibilityInternal)
	retired := write("Old driver", printers, model.VisibilityPublic)

	if _, err := k.faq.UpdateArticle(ctx, k.writer, retired.ID, command.FAQArticleCommand{Valid: ptr(false)}); err != nil {
		t.Fatal(err)
	}

	titles := func(reader service.FAQReader) []string {
		found, err := k.faq.Published(ctx, reader, "", 0)
		if err != nil {
			t.Fatalf("search the published knowledge base: %v", err)
		}

		names := make([]string, 0, len(found))
		for _, article := range found {
			names = append(names, article.Title)
		}

		slices.Sort(names)

		return names
	}

	if got := titles(service.Customer); !slices.Equal(got, []string{"Paper jam", "Toner swap"}) {
		t.Errorf("a customer finds %v; want what is meant for customers and what is public", got)
	}

	if got := titles(service.Anybody); !slices.Equal(got, []string{"Paper jam"}) {
		t.Errorf("anybody finds %v; want what is public alone", got)
	}

	if _, err := k.faq.PublishedArticle(ctx, service.Anybody, public.ID); err != nil {
		t.Errorf("anybody opening the public article: %v", err)
	}

	for _, refused := range []struct {
		reader  service.FAQReader
		article *model.FAQArticle
	}{
		{service.Anybody, external}, {service.Customer, internal}, {service.Customer, retired},
	} {
		_, err := k.faq.PublishedArticle(ctx, refused.reader, refused.article.ID)
		requireKind(t, err, apperror.KindNotFound)
	}

	categories, err := k.faq.PublishedCategories(ctx, service.Customer)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(categories))
	for _, category := range categories {
		names = append(names, category.Name)
	}

	slices.Sort(names)

	if !slices.Equal(names, []string{"Hardware", "Printers"}) {
		t.Errorf("a customer is shown the categories %v; want those holding what they may read and the one above",
			names)
	}
}
