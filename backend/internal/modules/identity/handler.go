package identity

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct {
	Svc          *Service
	CookieSecure bool
	CookieDomain string
	RefreshTTL   time.Duration
	// Shared, when set, makes the account rate limits count across API processes and restarts.
	Shared middleware.Counter
}

func (h *Handler) cookie(c fiber.Ctx, value string, maxAge int) {
	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    value,
		HTTPOnly: true,
		Secure:   h.CookieSecure,
		SameSite: "Strict",
		Path:     "/api/v1/auth",
		Domain:   h.CookieDomain,
		MaxAge:   maxAge,
	})
}

func (h *Handler) SetupStatus(c fiber.Ctx) error {
	needs, err := h.Svc.SetupStatus(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"data": fiber.Map{"needs_setup": needs}})
}

func (h *Handler) Setup(c fiber.Ctx) error {
	var body struct {
		BusinessName string `json:"business_name"`
		OwnerName    string `json:"owner_name"`
		Phone        string `json:"phone"`
		Email        string `json:"email"`
		Password     string `json:"password"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.Setup(c.Context(), SetupInput{
		BusinessName: body.BusinessName,
		OwnerName:    body.OwnerName,
		Phone:        body.Phone,
		Email:        body.Email,
		Password:     body.Password,
		IP:           c.IP(),
		UserAgent:    c.Get("User-Agent"),
	})
	if err != nil {
		return err
	}
	h.cookie(c, out.Refresh, int(h.RefreshTTL.Seconds()))
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(out))
}

func (h *Handler) Login(c fiber.Ctx) error {
	var body struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.Login(c.Context(), LoginInput{Identifier: body.Identifier, Password: body.Password,
		IP: c.IP(), UserAgent: c.Get("User-Agent")})
	if err != nil {
		return err
	}
	if out.Refresh != "" {
		h.cookie(c, out.Refresh, int(h.RefreshTTL.Seconds()))
	}
	return c.JSON(httpx.Data(out))
}

func (h *Handler) Refresh(c fiber.Ctx) error {
	raw := c.Cookies("refresh_token")
	out, err := h.Svc.Refresh(c.Context(), raw, c.IP(), c.Get("User-Agent"))
	if err != nil {
		h.cookie(c, "", -1)
		return err
	}
	h.cookie(c, out.Refresh, int(h.RefreshTTL.Seconds()))
	return c.JSON(httpx.Data(out))
}

func (h *Handler) Logout(c fiber.Ctx) error {
	if err := h.Svc.Logout(c.Context(), middleware.Principal(c), c.Cookies("refresh_token")); err != nil {
		return err
	}
	h.cookie(c, "", -1)
	return c.JSON(httpx.Data(fiber.Map{"ok": true}))
}

func (h *Handler) Reauthenticate(c fiber.Ctx) error {
	var body struct {
		Password string `json:"password"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	if body.Password == "" {
		return httpx.BadRequest("invalid_body", "Password is required")
	}
	out, err := h.Svc.Reauthenticate(c.Context(), middleware.Principal(c), body.Password)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(out))
}

func (h *Handler) Password(c fiber.Ctx) error {
	var body struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	if err := h.Svc.ChangePassword(c.Context(), middleware.Principal(c), body.Current, body.Next); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"ok": true}))
}

func (h *Handler) Me(c fiber.Ctx) error {
	data, err := h.Svc.Me(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(data))
}

func (h *Handler) UpdateMe(c fiber.Ctx) error {
	var body struct {
		Name     string `json:"name"`
		Language string `json:"language"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	if body.Language != "" && body.Language != "en" && body.Language != "sw" {
		return httpx.BadRequest("invalid_body", "Language must be en or sw")
	}
	if err := h.Svc.UpdateMe(c.Context(), middleware.Principal(c), body.Name, body.Language); err != nil {
		return err
	}
	return h.Me(c)
}

func (h *Handler) Sessions(c fiber.Ctx) error {
	list, err := h.Svc.ListSessions(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) EndSession(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.EndOwnSession(c.Context(), middleware.Principal(c), id); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"ok": true}))
}

func (h *Handler) ListUsers(c fiber.Ctx) error {
	list, total, err := h.Svc.ListUsers(c.Context(), middleware.Principal(c), c.Query("q"), httpx.PageFrom(c))
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	return c.JSON(httpx.List(list, httpx.Meta{Page: page.Page, PerPage: page.PerPage, Total: total}))
}

func (h *Handler) CreateUser(c fiber.Ctx) error {
	var body UserInput
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	u, err := h.Svc.CreateUser(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(u))
}

func (h *Handler) GetUser(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	u, err := h.Svc.GetUser(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(u))
}

func (h *Handler) UpdateUser(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body UserInput
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	u, err := h.Svc.UpdateUser(c.Context(), middleware.Principal(c), id, body)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(u))
}

func (h *Handler) setActive(active bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		if err := h.Svc.SetActive(c.Context(), middleware.Principal(c), id, active); err != nil {
			return err
		}
		return c.JSON(httpx.Data(fiber.Map{"ok": true}))
	}
}

func (h *Handler) ResetPassword(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	if err := h.Svc.ResetPassword(c.Context(), middleware.Principal(c), id, body.Password); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"ok": true}))
}

func (h *Handler) EndUserSessions(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.EndSessions(c.Context(), middleware.Principal(c), id); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"ok": true}))
}

func (h *Handler) GetUserPermissions(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetUserPermissions(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(out))
}

func (h *Handler) PutUserPermissions(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Overrides []UserPermissionOverride `json:"overrides"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.PutUserPermissions(c.Context(), middleware.Principal(c), id, body.Overrides)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(out))
}

func (h *Handler) LoginHistory(c fiber.Ctx) error {
	var userID *uuid.UUID
	if raw := c.Query("user_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return httpx.BadRequest("invalid_id", "user_id must be a UUID")
		}
		userID = &id
	}
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.LoginHistory(c.Context(), middleware.Principal(c), userID, from, to, page)
	if err != nil {
		return err
	}
	return c.JSON(httpx.List(list, httpx.Meta{Page: page.Page, PerPage: page.PerPage, Total: total}))
}

func (h *Handler) Permissions(c fiber.Ctx) error {
	list, err := h.Svc.PermissionCatalogue(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) MyLoginHistory(c fiber.Ctx) error {
	p := middleware.Principal(c)
	page := httpx.PageFrom(c)
	uid := p.UserID
	list, total, err := h.Svc.LoginHistory(c.Context(), p, &uid, "", "", page)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"data": list, "meta": httpx.Meta{Page: page.Page, PerPage: page.PerPage, Total: total}})
}

func (h *Handler) ListRoles(c fiber.Ctx) error {
	list, err := h.Svc.ListRoles(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) CreateRole(c fiber.Ctx) error {
	var body RoleInput
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	role, err := h.Svc.CreateRole(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(role))
}

func (h *Handler) GetRole(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	role, err := h.Svc.GetRole(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(role))
}

func (h *Handler) UpdateRole(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body RoleInput
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	role, err := h.Svc.UpdateRole(c.Context(), middleware.Principal(c), id, body)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(role))
}

func (h *Handler) DeleteRole(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.DeleteRole(c.Context(), middleware.Principal(c), id); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"ok": true}))
}

func RegisterPublic(r fiber.Router, h *Handler, limit fiber.Handler) {
	r.Post("/login", limit, h.Login)
	r.Post("/refresh", limit, h.Refresh)
}

func Register(r fiber.Router, h *Handler) {
	reauth := middleware.RequireRecentAuth(h.Svc.reauthWindow(), h.Svc.Clock.Now)
	userLimiter := middleware.NewLimiter(20, time.Minute)
	if h.Shared != nil {
		userLimiter.Share(h.Shared, "account")
	}
	userLimit := userLimiter.HandlerByUser()

	r.Post("/auth/logout", h.Logout)
	r.Post("/auth/reauthenticate", userLimit, h.Reauthenticate)
	r.Put("/auth/password", userLimit, h.Password)
	r.Get("/me", h.Me)
	r.Put("/me", h.UpdateMe)
	r.Get("/me/sessions", h.Sessions)
	r.Delete("/me/sessions/:id", h.EndSession)
	r.Get("/me/login-history", h.MyLoginHistory)
	r.Get("/users", middleware.Require("security.user.manage"), h.ListUsers)
	r.Post("/users", middleware.Require("security.user.manage"), h.CreateUser)
	r.Get("/users/:id", middleware.Require("security.user.manage"), h.GetUser)
	r.Put("/users/:id", middleware.Require("security.user.manage"), h.UpdateUser)
	r.Post("/users/:id/deactivate", middleware.Require("security.user.manage"), reauth, h.setActive(false))
	r.Post("/users/:id/activate", middleware.Require("security.user.manage"), h.setActive(true))
	r.Post("/users/:id/reset-password", middleware.Require("security.user.manage"), reauth, userLimit, h.ResetPassword)
	r.Delete("/users/:id/sessions", middleware.Require("security.user.manage"), h.EndUserSessions)
	r.Get("/users/:id/permissions", middleware.Require("security.user.manage"), h.GetUserPermissions)
	r.Put("/users/:id/permissions", middleware.Require("security.user.manage"), reauth, h.PutUserPermissions)
	r.Get("/login-history", middleware.Require("security.audit.view"), h.LoginHistory)
	r.Get("/permissions", middleware.Require("security.user.manage"), h.Permissions)
	r.Get("/roles", middleware.Require("security.user.manage"), h.ListRoles)
	r.Post("/roles", middleware.Require("security.user.manage"), reauth, h.CreateRole)
	r.Get("/roles/:id", middleware.Require("security.user.manage"), h.GetRole)
	r.Put("/roles/:id", middleware.Require("security.user.manage"), reauth, h.UpdateRole)
	r.Delete("/roles/:id", middleware.Require("security.user.manage"), reauth, h.DeleteRole)
}
