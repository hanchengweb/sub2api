package middleware

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsImageTaskRead(t *testing.T) {
	require.True(t, isImageTaskRead(http.MethodGet, "/v1/images/tasks/imgtask_123"))
	require.True(t, isImageTaskRead(http.MethodGet, "/images/tasks/imgtask_123"))
	require.True(t, isImageTaskRead(http.MethodGet, "/v1/images/generations/task_img_123"))
	require.True(t, isImageTaskRead(http.MethodGet, "/images/generations/task_img_123"))
	require.False(t, isImageTaskRead(http.MethodPost, "/v1/images/generations/task_img_123"))
	require.False(t, isImageTaskRead(http.MethodGet, "/v1/images/generations"))
}
