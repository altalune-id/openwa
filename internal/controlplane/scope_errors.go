package controlplane

import (
	"errors"
	"strings"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/openwa/gen/go/apperror/v1"
	"altalune.id/openwa/internal/apperror"
)

// ProjectUnresolvedError signals a request omitted project_id while its principal carries no active project.
type ProjectUnresolvedError struct{}

func (*ProjectUnresolvedError) Error() string {
	return "controlplane: project_id omitted and the principal carries no active project"
}

// ToAppError maps ProjectUnresolvedError to a FailedPrecondition envelope naming the way out.
func (*ProjectUnresolvedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeProjectUnresolved,
		"No project was given and this credential has no active project. Call project_list to get a projectId, then pass it as projectId.",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeProjectUnresolved,
			Meta: map[string]string{"field": "project_id"},
		},
	)
}

// IsProjectUnresolvedError reports whether err's tree contains a *ProjectUnresolvedError.
func IsProjectUnresolvedError(err error) bool {
	_, ok := errors.AsType[*ProjectUnresolvedError](err)
	return ok
}

// DeviceUnresolvedError signals a request omitted device_id while its project has no device or several.
type DeviceUnresolvedError struct{ Candidates []string }

func (e *DeviceUnresolvedError) Error() string {
	if len(e.Candidates) == 0 {
		return "controlplane: device_id omitted and the project has no device"
	}
	return "controlplane: device_id omitted and the project has several devices: " + strings.Join(e.Candidates, ", ")
}

// ToAppError maps DeviceUnresolvedError to an InvalidArgument envelope listing the candidates.
func (e *DeviceUnresolvedError) ToAppError() *apperror.AppError {
	if len(e.Candidates) == 0 {
		return apperror.New(
			apperror.CodeValidation,
			"This project has no device yet; create a device in the console or with POST /devices, then pass its id as device_id.",
			codes.InvalidArgument,
			&apperrorv1.ErrorDetail{Code: apperror.CodeValidation, Meta: map[string]string{"field": "device_id"}},
		)
	}
	candidates := strings.Join(e.Candidates, ", ")
	return apperror.New(
		apperror.CodeValidation,
		"This project has several devices; pass device_id as one of: "+candidates,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeValidation, Meta: map[string]string{"field": "device_id", "candidates": candidates}},
	)
}

// IsDeviceUnresolvedError reports whether err's tree contains a *DeviceUnresolvedError.
func IsDeviceUnresolvedError(err error) bool {
	_, ok := errors.AsType[*DeviceUnresolvedError](err)
	return ok
}
