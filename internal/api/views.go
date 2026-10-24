package api

import (
	"context"
	"encoding/json"

	"github.com/go-faster/jx"

	"freightflow/internal/api/gen"
	"freightflow/internal/views"
)

// --- Сохранённые виды ---

func toView(v views.View) (gen.SavedView, error) {
	params := gen.SavedViewParams{}
	for k, val := range v.Params {
		b, err := json.Marshal(val)
		if err != nil {
			return gen.SavedView{}, err
		}
		params[k] = jx.Raw(b)
	}
	out := gen.SavedView{
		ID: v.ID, Page: gen.SavedViewPage(v.Page), Name: v.Name, Params: params, Owner: v.Owner, Mine: v.Mine,
		CreatedAt: v.CreatedAt, Role: optStr(v.Role),
	}
	return out, nil
}

func (s *Server) ListViews(ctx context.Context, p gen.ListViewsParams) ([]gen.SavedView, error) {
	pr := principal(ctx)
	list, err := s.views.List(ctx, pr.UserID, pr.Roles, string(p.Page))
	if err != nil {
		return nil, err
	}
	out := make([]gen.SavedView, len(list))
	for i, v := range list {
		if out[i], err = toView(v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Server) CreateView(ctx context.Context, req *gen.CreateViewReq) (*gen.SavedView, error) {
	pr := principal(ctx)
	params := make(map[string]any, len(req.Params))
	for k, raw := range req.Params {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		params[k] = v
	}
	var role *string
	if r, ok := req.Role.Get(); ok && r != "" {
		role = &r
	}
	v, err := s.views.Create(ctx, pr.UserID, pr.Roles, string(req.Page), req.Name, params, role)
	if err != nil {
		return nil, err
	}
	out, err := toView(*v)
	return &out, err
}

func (s *Server) DeleteView(ctx context.Context, p gen.DeleteViewParams) error {
	return s.views.Delete(ctx, principal(ctx).UserID, p.ViewId)
}
