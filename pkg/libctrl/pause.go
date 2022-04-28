package libctrl

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/authzed/spicedb-operator/pkg/libctrl/handler"
)

const ConditionTypePaused = "Paused"

// HasStatusConditions is used for pausing
type HasStatusConditions interface {
	comparable
	metav1.Object
	FindStatusCondition(conditionType string) *metav1.Condition
	SetStatusCondition(metav1.Condition)
}

func IsPaused(object metav1.Object, pausedLabelKey string) bool {
	objLabels := object.GetLabels()
	if objLabels == nil {
		return false
	}

	_, ok := objLabels[pausedLabelKey]
	return ok
}

type PauseHandler[K HasStatusConditions] struct {
	ControlDoneRequeue
	PausedLabelKey string
	Object         K
	PatchStatus    func(ctx context.Context, patch K) error
	Next           handler.ContextHandler
}

func NewPauseHandler[K HasStatusConditions](ctrls ControlDoneRequeue,
	pausedLabelKey string,
	object K,
	patchStatus func(ctx context.Context, patch K) error,
	next handler.ContextHandler,
) *PauseHandler[K] {
	return &PauseHandler[K]{
		ControlDoneRequeue: ctrls,
		PausedLabelKey:     pausedLabelKey,
		Object:             object,
		PatchStatus:        patchStatus,
		Next:               next,
	}
}

func (p *PauseHandler[K]) pause(ctx context.Context) {
	if p.Object.FindStatusCondition(ConditionTypePaused) != nil {
		p.Done()
		return
	}
	p.Object.SetStatusCondition(NewPausedCondition(p.PausedLabelKey))
	if err := p.PatchStatus(ctx, p.Object); err != nil {
		p.Requeue()
		return
	}
	p.Done()
}

func (p *PauseHandler[K]) Handle(ctx context.Context) {
	if IsPaused(p.Object, p.PausedLabelKey) {
		p.pause(ctx)
		return
	}
	p.Next.Handle(ctx)
}

// SelfPauseHandler is used when the controller pauses itself. This is only
// used when the controller has no good way to tell when the bad state has
// been resolved (i.e. an external resource is behaving poorly).
type SelfPauseHandler[K HasStatusConditions] struct {
	HandlerControls
	CtxKey         *ContextDefaultingKey[K]
	PausedLabelKey string
	OwnerUID       types.UID
	Patch          func(ctx context.Context, patch K) error
	PatchStatus    func(ctx context.Context, patch K) error
}

func NewSelfPauseHandler[K HasStatusConditions](ctrls HandlerControls,
	pausedLabelKey string,
	contextKey *ContextDefaultingKey[K],
	ownerUID types.UID,
	patch, patchStatus func(ctx context.Context, patch K) error,
) *SelfPauseHandler[K] {
	return &SelfPauseHandler[K]{
		CtxKey:          contextKey,
		HandlerControls: ctrls,
		PausedLabelKey:  pausedLabelKey,
		OwnerUID:        ownerUID,
		Patch:           patch,
		PatchStatus:     patchStatus,
	}
}

func (p *SelfPauseHandler[K]) Handle(ctx context.Context) {
	object := p.CtxKey.MustValue(ctx)
	object.SetStatusCondition(NewSelfPausedCondition(p.PausedLabelKey))
	if err := p.PatchStatus(ctx, object); err != nil {
		p.RequeueErr(err)
		return
	}
	labels := object.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[p.PausedLabelKey] = string(p.OwnerUID)
	object.SetLabels(labels)
	if err := p.Patch(ctx, object); err != nil {
		utilruntime.HandleError(err)
		p.Requeue()
		return
	}
	p.Done()
}

func NewPausedCondition(pausedLabelKey string) metav1.Condition {
	return metav1.Condition{
		Type:               ConditionTypePaused,
		Status:             metav1.ConditionTrue,
		Reason:             "PausedByLabel",
		LastTransitionTime: metav1.NewTime(time.Now()),
		Message:            fmt.Sprintf("Controller pause requested via label: %s", pausedLabelKey),
	}
}

func NewSelfPausedCondition(pausedLabelKey string) metav1.Condition {
	return metav1.Condition{
		Type:               ConditionTypePaused,
		Status:             metav1.ConditionTrue,
		Reason:             "PausedByController",
		LastTransitionTime: metav1.NewTime(time.Now()),
		Message:            fmt.Sprintf("Reconiciliation has been paused by the controller; see other conditions for more information. When ready, unpause by removing the %s label", pausedLabelKey),
	}
}