package service

import (
	"context"
	"slices"
	"strconv"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// FAQReader is who reads the knowledge base from outside the staff: a customer
// signed in to the portal, or anybody at all.
type FAQReader bool

// The two readers outside the staff.
const (
	// Anybody reads what is public, signed in or not.
	Anybody FAQReader = false

	// Customer reads what is meant for customers signed in, and what is public.
	Customer FAQReader = true
)

// visibilities are what the reader may read, as KIX18's FAQ divides it.
func (r FAQReader) visibilities() []model.FAQVisibility {
	if r == Customer {
		return []model.FAQVisibility{model.VisibilityExternal, model.VisibilityPublic}
	}

	return []model.FAQVisibility{model.VisibilityPublic}
}

// Published is what a search takes of the articles the reader may read, the
// latest written first. Never a retired one and never an internal one, whoever
// asks: the staff read those through Search.
func (s *FAQApplicationService) Published(
	ctx context.Context,
	reader FAQReader,
	words string,
	categoryID uint,
) ([]*model.FAQArticle, error) {
	return s.articles.Search(ctx, repository.FAQFilter{
		Words: words, CategoryID: categoryID, Visibilities: reader.visibilities(),
		ValidOnly: true, Limit: model.MaxFAQArticlesListed,
	})
}

// PublishedArticle is one article the reader may read; any other is not found,
// as an article that is not there is not.
func (s *FAQApplicationService) PublishedArticle(
	ctx context.Context,
	reader FAQReader,
	id uint,
) (*model.FAQArticle, error) {
	article, err := s.articles.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if !article.Valid || !slices.Contains(reader.visibilities(), article.Visibility) {
		return nil, apperror.NotFound("faqarticle", strconv.FormatUint(uint64(id), 10))
	}

	return article, nil
}

// PublishedCategories are the categories the reader is shown: those holding an
// article they may read, and the ones above those, so that each can be named by
// its place in the tree. A category holding nothing they may read is not among
// them - its name alone can say more than an installation meant to publish.
func (s *FAQApplicationService) PublishedCategories(
	ctx context.Context,
	reader FAQReader,
) ([]*model.FAQCategory, error) {
	articles, err := s.Published(ctx, reader, "", 0)
	if err != nil {
		return nil, err
	}

	all, err := s.categories.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	byID := make(map[uint]*model.FAQCategory, len(all))
	for _, category := range all {
		byID[category.ID] = category
	}

	shown := map[uint]bool{}

	for _, article := range articles {
		for id := &article.CategoryID; id != nil && !shown[*id]; {
			category, found := byID[*id]
			if !found {
				break
			}

			shown[*id] = true
			id = category.ParentID
		}
	}

	return slices.DeleteFunc(all, func(category *model.FAQCategory) bool { return !shown[category.ID] }), nil
}
