package http

import (
	"net/http"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/app"
	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/platform/httpx"
)

// listTags godoc
//
//	@Summary	List tags
//	@Tags		tags
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	TagListEnvelope
//	@Failure	401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Router		/tags [get]
func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	tags, err := h.svc.ListTags(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toTagResponses(tags))
}

// createTag godoc
//
//	@Summary		Create tag
//	@Description	Name is unique per user (case-insensitive), max 30 characters; a leading # is stripped. Color #RRGGBB.
//	@Tags			tags
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		CreateTagRequest	true	"Tag"
//	@Success		201		{object}	TagEnvelope
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		409		{object}	httpx.ErrorResponse	"DUPLICATE_NAME"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Router			/tags [post]
func (h *Handler) createTag(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	req, err := decodeAndValidate[CreateTagRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	t, err := h.svc.CreateTag(r.Context(), uid, req.Name, req.Color)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusCreated, toTagResponse(t))
}

// updateTag godoc
//
//	@Summary	Update tag
//	@Tags		tags
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id		path		string				true	"Tag ID"	format(uuid)
//	@Param		body	body		UpdateTagRequest	true	"Fields to change"
//	@Success	200		{object}	TagEnvelope
//	@Failure	401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure	404		{object}	httpx.ErrorResponse	"TAG_NOT_FOUND"
//	@Failure	409		{object}	httpx.ErrorResponse	"DUPLICATE_NAME"
//	@Failure	422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED"
//	@Router		/tags/{id} [patch]
func (h *Handler) updateTag(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrTagNotFound)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	req, err := decodeAndValidate[UpdateTagRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	t, err := h.svc.UpdateTag(r.Context(), app.UpdateTagInput{UserID: uid, ID: id, Name: req.Name, Color: req.Color})
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toTagResponse(t))
}

// deleteTag godoc
//
//	@Summary		Delete tag
//	@Description	Deletes the tag and detaches it from all transactions.
//	@Tags			tags
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Tag ID"	format(uuid)
//	@Success		204
//	@Failure		401	{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404	{object}	httpx.ErrorResponse	"TAG_NOT_FOUND"
//	@Router			/tags/{id} [delete]
func (h *Handler) deleteTag(w http.ResponseWriter, r *http.Request) {
	h.deleteAction(w, r, domain.ErrTagNotFound, h.svc.DeleteTag)
}

// setTransactionTags godoc
//
//	@Summary		Replace transaction tags
//	@Description	Replaces all tags of a transaction (max 10). An empty list removes all tags.
//	@Tags			transactions
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		string						true	"Transaction ID"	format(uuid)
//	@Param			body	body		SetTransactionTagsRequest	true	"Tag IDs"
//	@Success		200		{object}	TagListEnvelope
//	@Failure		401		{object}	httpx.ErrorResponse	"UNAUTHENTICATED"
//	@Failure		404		{object}	httpx.ErrorResponse	"TRANSACTION_NOT_FOUND / TAG_NOT_FOUND"
//	@Failure		422		{object}	httpx.ErrorResponse	"VALIDATION_FAILED / TOO_MANY_TAGS"
//	@Router			/transactions/{id}/tags [put]
func (h *Handler) setTransactionTags(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, domain.ErrTransactionNotFound)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	req, err := decodeAndValidate[SetTransactionTagsRequest](h, w, r)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	ids, err := parseTagIDs(req.TagIDs)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	tags, err := h.svc.SetTransactionTags(r.Context(), uid, id, ids)
	if err != nil {
		httpx.WriteError(w, r, mapError(err))
		return
	}
	httpx.Data(w, http.StatusOK, toTagResponses(tags))
}

func parseTagIDs(vals []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(vals))
	for _, v := range vals {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, invalidParam("tag_ids", "harus UUID")
		}
		out = append(out, id)
	}
	return out, nil
}
