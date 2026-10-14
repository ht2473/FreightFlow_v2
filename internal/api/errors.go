package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	ogenerrors "github.com/ogen-go/ogen/ogenerrors"

	"freightflow/internal/api/gen"
	"freightflow/internal/facility"
	"freightflow/internal/identity"
	"freightflow/internal/ingest"
	"freightflow/internal/platform/problem"
	"freightflow/internal/report"
	"freightflow/internal/routing"
)

// NewError переводит ошибку обработчика в ответ RFC 9457.
func (s *Server) NewError(ctx context.Context, err error) *gen.ProblemStatusCode {
	p := s.toProblem(ctx, err)
	out := gen.Problem{Title: p.Title, Status: p.Status}
	if p.Detail != "" {
		out.Detail = gen.NewOptString(p.Detail)
	}
	if p.Code != "" {
		out.Code = gen.NewOptString(p.Code)
	}
	for _, fe := range p.Errors {
		out.Errors = append(out.Errors, gen.FieldError{Field: fe.Field, Message: fe.Message})
	}
	return &gen.ProblemStatusCode{StatusCode: p.Status, Response: out}
}

func (s *Server) toProblem(ctx context.Context, err error) *problem.Problem {
	var (
		ve        *identity.ValidationError
		pr        *problem.Problem
		secErr    *ogenerrors.SecurityError
		decodeErr *ogenerrors.DecodeRequestError
		paramsErr *ogenerrors.DecodeParamsError
	)
	switch {
	case errors.As(err, &pr):
		return pr
	case errors.As(err, &secErr):
		if errors.Is(err, errForbidden) {
			return problem.Forbidden
		}
		return problem.Unauthorized
	case errors.As(err, &decodeErr), errors.As(err, &paramsErr):
		return problem.BadRequest.WithDetail(err.Error())
	case errors.As(err, &ve):
		p := problem.New(http.StatusUnprocessableEntity, "validation", "Проверьте введённые данные")
		p.Errors = []problem.FieldError{{Field: ve.Field, Message: ve.Message}}
		return p
	case errors.Is(err, errUnauthorized):
		return problem.Unauthorized
	case errors.Is(err, errForbidden):
		return problem.Forbidden
	case errors.Is(err, errSessionOnly):
		return problem.Forbidden.WithDetail("Операция доступна только в веб-интерфейсе")
	case errors.Is(err, errAdminTOTPRequired):
		return problem.Forbidden.WithDetail("Для администраторов двухфакторная защита обязательна")
	case errors.Is(err, identity.ErrNotFound):
		return problem.NotFound
	case errors.Is(err, identity.ErrEmailTaken):
		return problem.New(http.StatusConflict, "email_taken", "Пользователь с таким адресом уже существует")
	case errors.Is(err, identity.ErrLastAdmin):
		return problem.New(http.StatusConflict, "last_admin", "Нельзя лишить прав последнего администратора")
	case errors.Is(err, routing.ErrNoRoute):
		return problem.New(http.StatusUnprocessableEntity, "no_route", "Маршрут для этого ТС не найден").
			WithDetail("Между точками нет пути с учётом ограничений и закрытых зон")
	case errors.Is(err, report.ErrNotFound):
		return problem.NotFound
	case errors.Is(err, report.ErrNotReady):
		return problem.New(http.StatusConflict, "export_not_ready", "Файл выгрузки ещё не готов")
	case errors.Is(err, facility.ErrNotFound):
		return problem.NotFound
	case errors.Is(err, facility.ErrInvalid):
		return problem.New(http.StatusUnprocessableEntity, "invalid_curation", "Проверьте данные").
			WithDetail(strings.TrimPrefix(err.Error(), facility.ErrInvalid.Error()+": "))
	case errors.Is(err, facility.ErrBadFilter):
		return problem.BadRequest.WithDetail(err.Error())
	case errors.Is(err, routing.ErrMatrixTooLarge):
		return problem.New(http.StatusUnprocessableEntity, "matrix_too_large", "Слишком большая матрица").
			WithDetail("Источников × целей — не более 2 500")
	case errors.Is(err, routing.ErrNoAnalysis):
		return problem.New(http.StatusNotFound, "no_analysis", "Сетевой анализ ещё не выполнен").
			WithDetail("Граф анализа строится после загрузки дорожной сети")
	case errors.Is(err, routing.ErrNoLocation):
		return problem.New(http.StatusUnprocessableEntity, "no_location", "Точка далеко от дорог, доступных этому ТС").
			WithDetail("Поставьте точку ближе к улице или проезду")
	case errors.Is(err, routing.ErrLimit):
		return problem.New(http.StatusUnprocessableEntity, "routing_limit", "Превышен предел службы маршрутизации").
			WithDetail(err.Error())
	case errors.Is(err, routing.ErrUnavailable):
		return problem.New(http.StatusServiceUnavailable, "routing_unavailable", "Служба маршрутизации недоступна")
	case errors.As(err, new(*routing.Error)):
		return problem.New(http.StatusBadGateway, "routing_error", "Служба маршрутизации отклонила запрос").WithDetail(err.Error())
	case errors.Is(err, ingest.ErrUnknownSource), errors.Is(err, ingest.ErrRunNotFound):
		return problem.NotFound
	case errors.Is(err, ingest.ErrRunActive):
		return problem.New(http.StatusConflict, "run_active", "Загрузка этого источника уже выполняется")
	case errors.Is(err, identity.ErrTOTPEnabled):
		return problem.New(http.StatusConflict, "totp_enabled", "Двухфакторная защита уже включена")
	case errors.Is(err, identity.ErrInvalidCode):
		p := problem.New(http.StatusUnprocessableEntity, "invalid_code", "Неверный код")
		p.Errors = []problem.FieldError{{Field: "code", Message: "Неверный код подтверждения"}}
		return p
	case errors.Is(err, identity.ErrInvalidCredentials):
		p := problem.New(http.StatusUnprocessableEntity, "invalid_password", "Неверный пароль")
		p.Errors = []problem.FieldError{{Field: "password", Message: "Пароль указан неверно"}}
		return p
	case errors.Is(err, context.Canceled):
		return problem.New(499, "canceled", "Запрос отменён")
	default:
		s.log.ErrorContext(ctx, "api handler error", "error", err)
		return problem.Internal
	}
}

// errorHandler отвечает на ошибки, которые ogen не передаёт в NewError.
func (s *Server) errorHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
	p := s.toProblem(ctx, err)
	if p == problem.Internal {
		if code := ogenerrors.ErrorCode(err); code < http.StatusInternalServerError {
			p = problem.New(code, "request_error", http.StatusText(code)).WithDetail(err.Error())
		}
	}
	problem.Write(w, r, p)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, r, problem.NotFound)
}

func (s *Server) methodNotAllowed(w http.ResponseWriter, r *http.Request, allowed string) {
	w.Header().Set("Allow", allowed)
	problem.Write(w, r, problem.New(http.StatusMethodNotAllowed, "method_not_allowed", "Метод не поддерживается"))
}
