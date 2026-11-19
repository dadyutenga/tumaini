package middleware

import (
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"

	"bms/internal/platform/httpx"
)

func app(l *Limiter) *fiber.App {
	a := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler})
	a.Get("/x", l.Handler(), func(c fiber.Ctx) error { return c.SendString("ok") })
	return a
}

func TestLimiterBlocksAfterMaxInWindow(t *testing.T) {
	a := app(NewLimiter(3, time.Minute))
	for i := 0; i < 3; i++ {
		resp, err := a.Test(httptest.NewRequest("GET", "/x", nil))
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
	}
	resp, err := a.Test(httptest.NewRequest("GET", "/x", nil))
	require.NoError(t, err)
	require.Equal(t, 429, resp.StatusCode)
}

func TestLimiterMapStaysBounded(t *testing.T) {
	l := NewLimiter(5, time.Millisecond)
	old := time.Now().Add(-time.Hour)
	for i := 0; i < limiterMaxKeys+50; i++ {
		l.m["ip"+strconv.Itoa(i)+"|/x"] = bucket{start: old, n: 1}
	}
	a := app(l)
	resp, err := a.Test(httptest.NewRequest("GET", "/x", nil))
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	require.Less(t, len(l.m), 100, "expired buckets are evicted once the map is full")
}
