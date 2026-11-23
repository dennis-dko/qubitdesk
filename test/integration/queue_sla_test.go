//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
)

// A queue names the service level its new tickets start with, over HTTP, and
// a service level a queue starts its tickets with is not deleted from under it.
func TestAQueueNamesItsServiceLevelOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")

	var level, queue struct {
		ID             uint  `json:"id"`
		ServiceLevelID *uint `json:"serviceLevelId"`
	}

	admin.must(admin.api(http.MethodPost, "/service-levels", map[string]any{"name": "Gold", "solutionMinutes": 480}),
		http.StatusCreated).Data(t, &level)

	admin.must(admin.api(http.MethodPost, "/queues", map[string]any{"name": "Escalations", "serviceLevelId": level.ID}),
		http.StatusCreated).Data(t, &queue)

	if queue.ServiceLevelID == nil || *queue.ServiceLevelID != level.ID {
		t.Errorf("the queue reads back as %+v", queue)
	}

	inUse := admin.api(http.MethodDelete, "/service-levels/"+strconv.FormatUint(uint64(level.ID), 10), nil)
	if inUse.Status != http.StatusConflict || errorCode(t, inUse) != "serviceLevelInUse" {
		t.Errorf("deleting the level a queue starts its tickets with was answered %d %s, want 409 serviceLevelInUse",
			inUse.Status, inUse.Body)
	}

	admin.must(admin.api(http.MethodPut, "/queues/"+strconv.FormatUint(uint64(queue.ID), 10),
		map[string]any{"clearServiceLevel": true}), http.StatusOK).Data(t, &queue)

	if queue.ServiceLevelID != nil {
		t.Errorf("after taking it off the queue reads %+v", queue)
	}

	admin.must(admin.api(http.MethodDelete, "/service-levels/"+strconv.FormatUint(uint64(level.ID), 10), nil),
		http.StatusNoContent, http.StatusOK)
}
