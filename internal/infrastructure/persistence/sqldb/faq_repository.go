package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// FAQCategoryRepository keeps the knowledge base's categories in
// faq_categories.
type FAQCategoryRepository struct{ base }

// NewFAQCategoryRepository creates the repository for the given dialect.
func NewFAQCategoryRepository(db DB, dialect string) *FAQCategoryRepository {
	return &FAQCategoryRepository{base{db: db, dialect: dialect}}
}

var _ repository.FAQCategoryRepository = (*FAQCategoryRepository)(nil)

const faqCategoryColumns = "id, parent_id, name, description, valid"

func scanFAQCategory(row interface{ Scan(dest ...any) error }) (*model.FAQCategory, error) {
	var (
		category model.FAQCategory
		parentID sql.NullInt64
	)

	if err := row.Scan(&category.ID, &parentID, &category.Name, &category.Description, &category.Valid); err != nil {
		return nil, err
	}

	if parentID.Valid {
		id := uint(parentID.Int64)
		category.ParentID = &id
	}

	return &category, nil
}

func (r *FAQCategoryRepository) GetAll(ctx context.Context) ([]*model.FAQCategory, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+faqCategoryColumns+" FROM faq_categories ORDER BY name")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	categories := make([]*model.FAQCategory, 0)

	for rows.Next() {
		category, scanErr := scanFAQCategory(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		categories = append(categories, category)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return categories, nil
}

func (r *FAQCategoryRepository) GetByID(ctx context.Context, id uint) (*model.FAQCategory, error) {
	category, err := scanFAQCategory(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+faqCategoryColumns+" FROM faq_categories WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("faqcategory", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return category, nil
}

func (r *FAQCategoryRepository) Save(ctx context.Context, category *model.FAQCategory) (*model.FAQCategory, error) {
	id, err := r.insert(ctx, "INSERT INTO faq_categories (parent_id, name, description, valid) VALUES (?, ?, ?, ?)",
		category.ParentID, category.Name, category.Description, category.Valid)
	if err != nil {
		return nil, translateFAQCategoryErr(err, category.Name)
	}

	return r.GetByID(ctx, id)
}

func (r *FAQCategoryRepository) Update(ctx context.Context, category *model.FAQCategory) (*model.FAQCategory, error) {
	found, err := r.update(ctx, "faq_categories",
		"UPDATE faq_categories SET parent_id = ?, name = ?, description = ?, valid = ? WHERE id = ?",
		category.ID, category.ParentID, category.Name, category.Description, category.Valid, category.ID)
	if err != nil {
		return nil, translateFAQCategoryErr(err, category.Name)
	}

	if !found {
		return nil, apperror.NotFound("faqcategory", strconv.FormatUint(uint64(category.ID), 10))
	}

	return r.GetByID(ctx, category.ID)
}

func (r *FAQCategoryRepository) Delete(ctx context.Context, id uint) error {
	affected, err := r.exec(ctx, "DELETE FROM faq_categories WHERE id = ?", id)
	if err != nil {
		return apperror.Internal(err)
	}

	if affected == 0 {
		return apperror.NotFound("faqcategory", strconv.FormatUint(uint64(id), 10))
	}

	return nil
}

func (r *FAQCategoryRepository) CountArticles(ctx context.Context, id uint) (int, error) {
	return r.count(ctx, "SELECT COUNT(*) FROM faq_articles WHERE category_id = ?", id)
}

func (r *FAQCategoryRepository) CountChildren(ctx context.Context, id uint) (int, error) {
	return r.count(ctx, "SELECT COUNT(*) FROM faq_categories WHERE parent_id = ?", id)
}

func (r *FAQCategoryRepository) count(ctx context.Context, query string, id uint) (int, error) {
	var count int

	if err := r.db.QueryRowContext(ctx, r.rebind(query), id).Scan(&count); err != nil {
		return 0, apperror.Internal(err)
	}

	return count, nil
}

func translateFAQCategoryErr(err error, name string) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("a category named %q already exists", name).WithCode("faqCategoryNameTaken", name)
	}

	return apperror.Internal(err)
}

// FAQArticleRepository keeps the knowledge base's articles in faq_articles.
type FAQArticleRepository struct{ base }

// NewFAQArticleRepository creates the repository for the given dialect.
func NewFAQArticleRepository(db DB, dialect string) *FAQArticleRepository {
	return &FAQArticleRepository{base{db: db, dialect: dialect}}
}

var _ repository.FAQArticleRepository = (*FAQArticleRepository)(nil)

const faqArticleColumns = "id, category_id, title, body, keywords, visibility, language, valid, created_at, " +
	"updated_at, updated_by"

func scanFAQArticle(row interface{ Scan(dest ...any) error }) (*model.FAQArticle, error) {
	var (
		article    model.FAQArticle
		visibility string
		created    dateTime
		updated    dateTime
		updatedBy  sql.NullInt64
	)

	if err := row.Scan(&article.ID, &article.CategoryID, &article.Title, &article.Body, &article.Keywords,
		&visibility, &article.Language, &article.Valid, &created, &updated, &updatedBy); err != nil {
		return nil, err
	}

	article.Visibility = model.FAQVisibility(visibility)
	article.CreatedAt, article.UpdatedAt = created.Time, updated.Time

	if updatedBy.Valid {
		id := uint(updatedBy.Int64)
		article.UpdatedBy = &id
	}

	return &article, nil
}

func (r *FAQArticleRepository) Search(ctx context.Context, filter repository.FAQFilter) ([]*model.FAQArticle, error) {
	if len(filter.Visibilities) == 0 {
		return []*model.FAQArticle{}, nil
	}

	visibilities := make([]any, 0, len(filter.Visibilities))
	for _, visibility := range filter.Visibilities {
		visibilities = append(visibilities, string(visibility))
	}

	where := []string{"visibility IN " + marks(len(visibilities))}
	args := visibilities

	if filter.CategoryID != 0 {
		where = append(where, "category_id = ?")
		args = append(args, filter.CategoryID)
	}

	if filter.ValidOnly {
		where = append(where, "valid = ?")
		args = append(args, true)
	}

	if words := strings.TrimSpace(filter.Words); words != "" {
		where = append(where, "(LOWER(title) LIKE ? ESCAPE '!' OR LOWER(keywords) LIKE ? ESCAPE '!' "+
			"OR LOWER(body) LIKE ? ESCAPE '!')")
		pattern := "%" + strings.ToLower(likeEscaped(words)) + "%"
		args = append(args, pattern, pattern, pattern)
	}

	args = append(args, filter.Limit)

	rows, err := r.db.QueryContext(ctx, r.rebind("SELECT "+faqArticleColumns+" FROM faq_articles WHERE "+
		strings.Join(where, " AND ")+" ORDER BY updated_at DESC, id DESC LIMIT ?"), args...)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	articles := make([]*model.FAQArticle, 0)

	for rows.Next() {
		article, scanErr := scanFAQArticle(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		articles = append(articles, article)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return articles, nil
}

func (r *FAQArticleRepository) GetByID(ctx context.Context, id uint) (*model.FAQArticle, error) {
	article, err := scanFAQArticle(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+faqArticleColumns+" FROM faq_articles WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("faqarticle", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return article, nil
}

func (r *FAQArticleRepository) Save(ctx context.Context, article *model.FAQArticle) (*model.FAQArticle, error) {
	id, err := r.insert(ctx,
		"INSERT INTO faq_articles (category_id, title, body, keywords, visibility, language, valid, created_at, "+
			"updated_at, updated_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		article.CategoryID, article.Title, article.Body, article.Keywords, string(article.Visibility), article.Language,
		article.Valid, article.CreatedAt.UTC(), article.UpdatedAt.UTC(), article.UpdatedBy)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	return r.GetByID(ctx, id)
}

func (r *FAQArticleRepository) Update(ctx context.Context, article *model.FAQArticle) (*model.FAQArticle, error) {
	found, err := r.update(ctx, "faq_articles",
		"UPDATE faq_articles SET category_id = ?, title = ?, body = ?, keywords = ?, visibility = ?, language = ?, "+
			"valid = ?, updated_at = ?, updated_by = ? WHERE id = ?",
		article.ID, article.CategoryID, article.Title, article.Body, article.Keywords, string(article.Visibility),
		article.Language, article.Valid, article.UpdatedAt.UTC(), article.UpdatedBy, article.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	if !found {
		return nil, apperror.NotFound("faqarticle", strconv.FormatUint(uint64(article.ID), 10))
	}

	return r.GetByID(ctx, article.ID)
}

func (r *FAQArticleRepository) Delete(ctx context.Context, id uint) error {
	affected, err := r.exec(ctx, "DELETE FROM faq_articles WHERE id = ?", id)
	if err != nil {
		return apperror.Internal(err)
	}

	if affected == 0 {
		return apperror.NotFound("faqarticle", strconv.FormatUint(uint64(id), 10))
	}

	return nil
}
