package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/gen1us2k/cloudnative_todo_list/config"
	"github.com/gen1us2k/cloudnative_todo_list/database"
	"github.com/gen1us2k/cloudnative_todo_list/database/supabase"
	"github.com/gen1us2k/cloudnative_todo_list/grpc/v1/todolist"
	"github.com/gen1us2k/cloudnative_todo_list/middleware"
	"github.com/gen1us2k/cloudnative_todo_list/models"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

type Server struct {
	db       database.Database
	config   *config.AppConfig
	errGroup *errgroup.Group
}

func NewServer(c *config.AppConfig) (*Server, error) {
	db, err := supabase.NewSupabaseDatabase(c)
	if err != nil {
		return nil, err
	}
	return &Server{db: db, config: c, errGroup: new(errgroup.Group)}, nil
}

func (s *Server) CreateTodo(ctx context.Context, todo *todolist.Todo) (*todolist.Todo, error) {
	t := models.NewTodoFromPB(todo)
	t, err := s.db.CreateTodo(t)
	if err != nil {
		return nil, err
	}
	return t.ToProto(), nil

}

func (s *Server) ListTodos(ctx context.Context, e *emptypb.Empty) (*todolist.TodoListResponse, error) {
	todos, err := s.db.ListTodos()
	if err != nil {
		return nil, err
	}
	res := &todolist.TodoListResponse{}
	for _, todo := range todos {
		res.Todos = append(res.Todos, todo.ToProto())
	}
	return res, nil
}

func (s *Server) UpdateTodo(ctx context.Context, todo *todolist.Todo) (*todolist.Todo, error) {
	t := models.NewTodoFromPB(todo)
	t, err := s.db.UpdateTodo(t)
	if err != nil {
		return nil, err
	}
	return t.ToProto(), nil
}

func (s *Server) DeleteTodo(ctx context.Context, todo *todolist.Todo) (*todolist.DeleteResponse, error) {
	if err := s.db.DeleteTodo(models.NewTodoFromPB(todo)); err != nil {
		return nil, err
	}

	return &todolist.DeleteResponse{Status: "success"}, nil
}
func (s *Server) startHTTP() error {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	gwmux := runtime.NewServeMux(
		runtime.WithForwardResponseOption(middleware.GatewayResponseModifier),
		runtime.WithMetadata(middleware.GatewayMetadataAnnotator),
	)
	kratos := middleware.KratosMiddleware{
		APIURL: s.config.KratosAPIURL,
		UIURL:  s.config.KratosUIURL,
		Client: &http.Client{},
	}

	r := mux.NewRouter()
	r.Use(kratos.Middleware)
	r.PathPrefix("/").Handler(gwmux)
	conn, err := grpc.DialContext(
		context.Background(),
		fmt.Sprintf("localhost:%d", s.config.GRPCPort),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return err
	}
	err = todolist.RegisterTodolistAPIServiceHandler(ctx, gwmux, conn)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.HTTPPort),
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      r, // Pass our instance of gorilla/mux in.
	}
	return srv.ListenAndServe()
}
func (s *Server) startGRPC() error {
	lis, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", s.config.GRPCPort))
	if err != nil {
		return err
	}
	var opts []grpc.ServerOption
	grpcServer := grpc.NewServer(opts...)
	todolist.RegisterTodolistAPIServiceServer(grpcServer, s)
	return grpcServer.Serve(lis)
}
func (s *Server) Start() {
	s.errGroup.Go(s.startGRPC)
	s.errGroup.Go(s.startHTTP)
}
func (s *Server) Wait() error {
	return s.errGroup.Wait()
}
