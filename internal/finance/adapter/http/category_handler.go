package http

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/httpx"
)

// listCategories godoc
//
//	@Summary		List categories
//	@Description	Category tree (root categories with their children): system categories plus the user's custom ones.
//	@Tags			categories
//	@Produce		json
//	@Security		BearerAuth
//	@Param			type	query		string	false	"Filter by type"	Enums(income, expense)
//	@Success		200		{object}	CategoryTreeEnvelope
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		422		{object}	httpx.ErrorResponse	"INVALID_TYPE"
//	@Router			/categories [get]
func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	nodes, err := h.svc.ListCategories(r.Context(), uid, r.URL.Query().Get("type"))
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toCategoryTree(nodes))
}

// getCategory godoc
//
//	@Summary	Get category
//	@Tags		categories
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Category ID"	format(uuid)
//	@Success	200	{object}	CategoryEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404	{object}	httpx.ErrorResponse	"CATEGORY_NOT_FOUND"
//	@Router		/categories/{id} [get]
func (h *Handler) getCategory(w http.ResponseWriter, r *http.Request) {
	h.categoryAction(w, r, http.StatusOK, func(uid, id uuid.UUID) (*domain.Category, error) {
		return h.svc.GetCategory(r.Context(), uid, id)
	})
}

// createCategory godoc
//
//	@Summary		Create category
//	@Description	Creates a custom category. parent_id must be a root category (system or own) of the same type: only one nesting level.
//	@Tags			categories
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		CreateCategoryRequest	true	"Category"
//	@Success		201		{object}	CategoryEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404		{object}	httpx.ErrorResponse	"CATEGORY_NOT_FOUND (parent)"
//	@Failure		409		{object}	httpx.ErrorResponse	"DUPLICATE_NAME"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / CATEGORY_NESTING_TOO_DEEP / CATEGORY_TYPE_MISMATCH"
//	@Router			/categories [post]
func (h *Handler) createCategory(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	req, err := decodeAndValidate[CreateCategoryRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	var parent *string
	if req.ParentID != "" {
		parent = &req.ParentID
	}
	parentID, err := parseUUIDPtr("parent_id", parent)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.svc.CreateCategory(r.Context(), app.CreateCategoryInput{UserID: uid, Type: req.Type, Name: req.Name,
		ParentID: parentID, Icon: req.Icon, Color: req.Color})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusCreated, toCategoryResponse(c))
}

// updateCategory godoc
//
//	@Summary		Update category
//	@Description	Renames or restyles a custom category. System categories are read-only.
//	@Tags			categories
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		string					true	"Category ID"	format(uuid)
//	@Param			body	body		UpdateCategoryRequest	true	"Fields to change"
//	@Success		200		{object}	CategoryEnvelope
//	@Failure		400		{object}	httpx.ErrorResponse	"INVALID_JSON"
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404		{object}	httpx.ErrorResponse	"CATEGORY_NOT_FOUND"
//	@Failure		409		{object}	httpx.ErrorResponse	"DUPLICATE_NAME"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / CATEGORY_READ_ONLY"
//	@Router			/categories/{id} [patch]
func (h *Handler) updateCategory(w http.ResponseWriter, r *http.Request) {
	h.categoryAction(w, r, http.StatusOK, func(uid, id uuid.UUID) (*domain.Category, error) {
		req, err := decodeAndValidate[UpdateCategoryRequest](h, w, r)
		if err != nil {
			return nil, err
		}
		return h.svc.UpdateCategory(r.Context(), app.UpdateCategoryInput{UserID: uid, ID: id,
			Name: req.Name, Icon: req.Icon, Color: req.Color})
	})
}

// deleteCategory godoc
//
//	@Summary		Delete category
//	@Description	Soft deletes a custom category. If transactions still use it, pass reassign_to to move them to another category of the same type first.
//	@Tags			categories
//	@Security		BearerAuth
//	@Param			id			path	string	true	"Category ID"								format(uuid)
//	@Param			reassign_to	query	string	false	"Target category for existing transactions"	format(uuid)
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorResponse	"INVALID_PARAMETER"
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404	{object}	httpx.ErrorResponse	"CATEGORY_NOT_FOUND"
//	@Failure		409	{object}	httpx.ErrorResponse	"CATEGORY_IN_USE / CATEGORY_HAS_CHILDREN"
//	@Failure		422	{object}	httpx.ErrorResponse	"CATEGORY_READ_ONLY / INVALID_REASSIGN_TARGET"
//	@Router			/categories/{id} [delete]
func (h *Handler) deleteCategory(w http.ResponseWriter, r *http.Request) {
	var reassign *uuid.UUID
	if v := r.URL.Query().Get("reassign_to"); v != "" {
		id, err := parseUUIDPtr("reassign_to", &v)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		reassign = id
	}
	h.deleteAction(w, r, domain.ErrCategoryNotFound, func(ctx context.Context, uid, id uuid.UUID) error {
		return h.svc.DeleteCategory(ctx, uid, id, reassign)
	})
}

func (h *Handler) categoryAction(w http.ResponseWriter, r *http.Request, status int, fn func(uid, id uuid.UUID) (*domain.Category, error)) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrCategoryNotFound)
	if err == nil {
		var c *domain.Category
		if c, err = fn(uid, id); err == nil {
			httpx.Data(w, status, toCategoryResponse(c))
			return
		}
	}
	httpx.WriteError(w, r, mapError(err))
}
