// Package validator membungkus go-playground/validator dan mengubah hasilnya
// menjadi detail error per field.
package validator

import (
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"

	"go-auth-clean/internal/platform/httpx"
)

type Validator struct {
	v *validator.Validate
}

func New() *Validator {
	v := validator.New(validator.WithRequiredStructEnabled())
	// Pakai nama dari tag json supaya pesan error sesuai field yang dikirim client.
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			return ""
		}
		return name
	})
	return &Validator{v: v}
}

func (v *Validator) Struct(s any) error {
	err := v.v.Struct(s)
	if err == nil {
		return nil
	}
	verrs, ok := errors.AsType[validator.ValidationErrors](err)
	if !ok {
		return err
	}
	details := make([]httpx.FieldDetail, 0, len(verrs))
	for _, fe := range verrs {
		details = append(details, httpx.FieldDetail{Field: fe.Field(), Message: message(fe)})
	}
	return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "VALIDATION_FAILED", Message: "input tidak valid", Details: details}
}

func message(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "wajib diisi"
	case "email":
		return "format email tidak valid"
	case "min":
		return "minimal " + fe.Param() + " karakter"
	case "max":
		return "maksimal " + fe.Param() + " karakter"
	default:
		return "tidak valid (" + fe.Tag() + ")"
	}
}
