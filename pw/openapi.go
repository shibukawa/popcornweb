package pw

import (
	"net/http"

	tinybind "github.com/shibukawa/tinybind-go/httpbind"
)

type OpenAPIInfo = tinybind.OpenAPIInfo

func SetOpenAPIInfo(info OpenAPIInfo) error { return tinybind.SetOpenAPIInfo(info) }

func AssembleOpenAPI() ([]byte, error) { return tinybind.AssembleOpenAPI() }

func OpenAPIJSON(w http.ResponseWriter, r *http.Request) { tinybind.OpenAPIJSON(w, r) }
