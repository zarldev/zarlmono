package grpc

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/repository"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
)

// Persons — CRUD-ish surface for face-recognized person records.

func (a *AdminServer) ListPersons(ctx context.Context, req *connect.Request[zarlv1.ListPersonsRequest]) (*connect.Response[zarlv1.ListPersonsResponse], error) {
	persons, err := a.persons.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list persons: %w", err))
	}
	msgs := make([]*zarlv1.PersonMsg, len(persons))
	for i, p := range persons {
		msgs[i] = &zarlv1.PersonMsg{
			Id:    string(p.ID),
			Name:  p.Name,
			Notes: p.Notes,
			Photo: p.Photo,
		}
	}
	return connect.NewResponse(&zarlv1.ListPersonsResponse{Persons: msgs}), nil
}

func (a *AdminServer) UpdatePerson(ctx context.Context, req *connect.Request[zarlv1.UpdatePersonRequest]) (*connect.Response[zarlv1.UpdatePersonResponse], error) {
	if err := a.persons.UpdateNotes(ctx, repository.PersonID(req.Msg.Id), req.Msg.Name, req.Msg.Notes); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update person: %w", err))
	}
	return connect.NewResponse(&zarlv1.UpdatePersonResponse{}), nil
}

func (a *AdminServer) DeletePerson(ctx context.Context, req *connect.Request[zarlv1.DeletePersonRequest]) (*connect.Response[zarlv1.DeletePersonResponse], error) {
	if err := a.persons.Delete(ctx, repository.PersonID(req.Msg.Id)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("delete person: %w", err))
	}
	return connect.NewResponse(&zarlv1.DeletePersonResponse{}), nil
}
