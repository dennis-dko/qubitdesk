package service

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// FAQApplicationService keeps the knowledge base, as KIX18 keeps its FAQ: a tree
// of categories and the articles filed under them, each saying who may read it.
// The staff read every visibility; whoever writes the knowledge base also sees
// what is retired, which is hidden from everybody else - a retired article is
// one that has stopped being true, and only its writers need it.
type FAQApplicationService struct {
	categories repository.FAQCategoryRepository
	articles   repository.FAQArticleRepository
	users      repository.UserRepository
	now        func() time.Time
}

// NewFAQApplicationService reads the names of whoever last wrote an article
// through users.
func NewFAQApplicationService(
	categories repository.FAQCategoryRepository,
	articles repository.FAQArticleRepository,
	users repository.UserRepository,
) *FAQApplicationService {
	return &FAQApplicationService{categories: categories, articles: articles, users: users, now: time.Now}
}

// WithClock reads the time from now instead of the wall.
func (s *FAQApplicationService) WithClock(now func() time.Time) *FAQApplicationService {
	s.now = now

	return s
}

// Categories is every category, the retired among them.
func (s *FAQApplicationService) Categories(ctx context.Context) ([]*model.FAQCategory, error) {
	return s.categories.GetAll(ctx)
}

func (s *FAQApplicationService) CreateCategory(ctx context.Context, cmd command.FAQCategoryCommand) (*model.FAQCategory, error) {
	if cmd.Name == nil {
		return nil, apperror.InvalidFields("name")
	}

	category := &model.FAQCategory{Valid: true}
	if err := s.applyCategory(ctx, category, cmd); err != nil {
		return nil, err
	}

	return s.categories.Save(ctx, category)
}

func (s *FAQApplicationService) UpdateCategory(
	ctx context.Context,
	id uint,
	cmd command.FAQCategoryCommand,
) (*model.FAQCategory, error) {
	category, err := s.categories.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.applyCategory(ctx, category, cmd); err != nil {
		return nil, err
	}

	return s.categories.Update(ctx, category)
}

// DeleteCategory deletes a category that holds no article and no other
// category; one that does is refused with how many, and is retired instead.
func (s *FAQApplicationService) DeleteCategory(ctx context.Context, id uint) error {
	category, err := s.categories.GetByID(ctx, id)
	if err != nil {
		return err
	}

	articles, err := s.categories.CountArticles(ctx, id)
	if err != nil {
		return err
	}

	if articles > 0 {
		return apperror.Conflictf("%d article(s) are filed under %q; make it invalid instead of deleting it",
			articles, category.Name).WithCode("faqCategoryInUse", category.Name, articles)
	}

	children, err := s.categories.CountChildren(ctx, id)
	if err != nil {
		return err
	}

	if children > 0 {
		return apperror.Conflictf("%d categor(ies) sit under %q; move or delete them first", children, category.Name).
			WithCode("faqCategoryHasChildren", category.Name, children)
	}

	return s.categories.Delete(ctx, id)
}

// applyCategory writes a command into a category, refusing every field it
// cannot hold at once, and a parent that would put it under itself.
func (s *FAQApplicationService) applyCategory(
	ctx context.Context,
	category *model.FAQCategory,
	cmd command.FAQCategoryCommand,
) error {
	var invalid []string

	name := strings.TrimSpace(valueOf(cmd.Name))
	if cmd.Name != nil && (name == "" || model.TooLong(name, model.MaxNameLength)) {
		invalid = append(invalid, "name")
	}

	description := strings.TrimSpace(valueOf(cmd.Description))
	if model.TooLong(description, model.MaxDescriptionLength) {
		invalid = append(invalid, "description")
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	if cmd.Name != nil {
		category.Name = name
	}

	if cmd.Description != nil {
		category.Description = description
	}

	if cmd.Valid != nil {
		category.Valid = *cmd.Valid
	}

	switch {
	case cmd.ClearParent:
		category.ParentID = nil
	case cmd.ParentID != nil:
		parent := *cmd.ParentID
		category.ParentID = &parent
	}

	return s.placeable(ctx, category)
}

// placeable refuses a parent that is not there, and one that would put the
// category under itself - walking up from the parent must end at the top
// without passing it, and stops at a category it has passed already, as the
// queues' walk does.
func (s *FAQApplicationService) placeable(ctx context.Context, category *model.FAQCategory) error {
	passed := map[uint]bool{}

	for at := category.ParentID; at != nil; {
		if (category.ID != 0 && *at == category.ID) || passed[*at] {
			return apperror.Invalidf("%q cannot sit under itself or under a category below it", category.Name).
				WithCode("faqCategoryUnderItself", category.Name)
		}

		passed[*at] = true

		parent, err := s.categories.GetByID(ctx, *at)
		if err != nil {
			if apperror.KindOf(err) == apperror.KindNotFound {
				return apperror.InvalidFields("parentId")
			}

			return err
		}

		at = parent.ParentID
	}

	return nil
}

// Search is the articles the caller may read that a search takes, the latest
// written first: every visibility for the staff, and the retired ones only for
// whoever writes the knowledge base.
func (s *FAQApplicationService) Search(
	ctx context.Context,
	caller *Principal,
	words string,
	categoryID uint,
) ([]*model.FAQArticle, error) {
	return s.articles.Search(ctx, repository.FAQFilter{
		Words: words, CategoryID: categoryID, Visibilities: model.FAQVisibilities(),
		ValidOnly: !writesFAQ(caller), Limit: model.MaxFAQArticlesListed,
	})
}

// Article is one article the caller may read; a retired one is not found for
// anybody who does not write the knowledge base.
func (s *FAQApplicationService) Article(ctx context.Context, caller *Principal, id uint) (*model.FAQArticle, error) {
	article, err := s.articles.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if !article.Valid && !writesFAQ(caller) {
		return nil, apperror.NotFound("faqarticle", strconv.FormatUint(uint64(id), 10))
	}

	return article, nil
}

func (s *FAQApplicationService) CreateArticle(
	ctx context.Context,
	caller *Principal,
	cmd command.FAQArticleCommand,
) (*model.FAQArticle, error) {
	var missing []string

	for _, field := range []struct {
		name string
		sent bool
	}{{"categoryId", cmd.CategoryID != nil}, {"title", cmd.Title != nil}, {"body", cmd.Body != nil}} {
		if !field.sent {
			missing = append(missing, field.name)
		}
	}

	if len(missing) > 0 {
		return nil, apperror.InvalidFields(missing...)
	}

	now := s.now().UTC()
	article := &model.FAQArticle{Valid: true, Visibility: model.VisibilityInternal, CreatedAt: now}

	if err := s.applyArticle(ctx, article, cmd); err != nil {
		return nil, err
	}

	article.UpdatedAt, article.UpdatedBy = now, callerID(caller)

	return s.articles.Save(ctx, article)
}

func (s *FAQApplicationService) UpdateArticle(
	ctx context.Context,
	caller *Principal,
	id uint,
	cmd command.FAQArticleCommand,
) (*model.FAQArticle, error) {
	article, err := s.articles.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.applyArticle(ctx, article, cmd); err != nil {
		return nil, err
	}

	article.UpdatedAt, article.UpdatedBy = s.now().UTC(), callerID(caller)

	return s.articles.Update(ctx, article)
}

func (s *FAQApplicationService) DeleteArticle(ctx context.Context, id uint) error {
	if id == 0 {
		return apperror.InvalidFields("id")
	}

	return s.articles.Delete(ctx, id)
}

// Names are the names of every account by id, for the articles' last writers:
// an id without a key is read as a name when it is shown.
func (s *FAQApplicationService) Names(ctx context.Context) (map[uint]string, error) {
	users, err := s.users.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	names := make(map[uint]string, len(users))
	for _, user := range users {
		names[user.ID] = user.Name
	}

	return names, nil
}

// applyArticle writes a command into an article, refusing every field it cannot
// hold at once: a category that is not there, or retired where it is named
// anew; a title or a text missing or too long; a visibility or a language there
// is not.
func (s *FAQApplicationService) applyArticle(
	ctx context.Context,
	article *model.FAQArticle,
	cmd command.FAQArticleCommand,
) error {
	var invalid []string

	if cmd.CategoryID != nil {
		category, err := s.categories.GetByID(ctx, *cmd.CategoryID)
		if err != nil && apperror.KindOf(err) != apperror.KindNotFound {
			return err
		}

		if category == nil || (!category.Valid && category.ID != article.CategoryID) {
			invalid = append(invalid, "categoryId")
		}
	}

	title := strings.TrimSpace(valueOf(cmd.Title))
	if cmd.Title != nil && (title == "" || model.TooLong(title, model.MaxTicketTitleLength)) {
		invalid = append(invalid, "title")
	}

	body := strings.TrimSpace(valueOf(cmd.Body))
	if cmd.Body != nil && (body == "" || model.TooLong(body, model.MaxFAQBodyLength)) {
		invalid = append(invalid, "body")
	}

	keywords := strings.TrimSpace(valueOf(cmd.Keywords))
	if model.TooLong(keywords, model.MaxNameLength) {
		invalid = append(invalid, "keywords")
	}

	visibility := model.FAQVisibility(strings.TrimSpace(valueOf(cmd.Visibility)))
	if cmd.Visibility != nil && !slices.Contains(model.FAQVisibilities(), visibility) {
		invalid = append(invalid, "visibility")
	}

	language := strings.TrimSpace(valueOf(cmd.Language))
	if language != "" && !model.IsSupportedLanguage(language) {
		invalid = append(invalid, "language")
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	if cmd.CategoryID != nil {
		article.CategoryID = *cmd.CategoryID
	}

	if cmd.Title != nil {
		article.Title = title
	}

	if cmd.Body != nil {
		article.Body = body
	}

	if cmd.Keywords != nil {
		article.Keywords = keywords
	}

	if cmd.Visibility != nil {
		article.Visibility = visibility
	}

	if cmd.Language != nil {
		article.Language = language
	}

	if cmd.Valid != nil {
		article.Valid = *cmd.Valid
	}

	return nil
}

// writesFAQ says whether the caller writes the knowledge base, and so sees what
// is retired in it.
func writesFAQ(caller *Principal) bool {
	return caller == nil || caller.Can(model.PermFAQWrite)
}
