package deletion

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"

	"github.com/grafana/loki/pkg/storage/chunk/client/local"
	"github.com/grafana/loki/pkg/storage/stores/indexshipper/storage"
)

func TestDeleteRequestsStore(t *testing.T) {
	now := model.Now()
	user1 := "user1"
	user2 := "user2"

	// build some test requests to add to the store
	var user1ExpectedRequests []DeleteRequest
	var user2ExpectedRequests []DeleteRequest
	for i := time.Duration(1); i <= 24; i++ {
		user1ExpectedRequests = append(user1ExpectedRequests, DeleteRequest{
			UserID:    user1,
			StartTime: now.Add(-i * time.Hour),
			EndTime:   now.Add(-i * time.Hour).Add(30 * time.Minute),
			CreatedAt: now.Add(-i * time.Hour).Add(30 * time.Minute),
			Query:     fmt.Sprintf(`{foo="%d", user="%s"}`, i, user1),
			Status:    StatusReceived,
		})
		user2ExpectedRequests = append(user2ExpectedRequests, DeleteRequest{
			UserID:    user2,
			StartTime: now.Add(-i * time.Hour),
			EndTime:   now.Add(-(i + 1) * time.Hour),
			CreatedAt: now.Add(-(i + 1) * time.Hour),
			Query:     fmt.Sprintf(`{foo="%d", user="%s"}`, i, user2),
			Status:    StatusReceived,
		})
	}

	// build the store
	tempDir := t.TempDir()

	workingDir := filepath.Join(tempDir, "working-dir")
	objectStorePath := filepath.Join(tempDir, "object-store")

	objectClient, err := local.NewFSObjectClient(local.FSConfig{
		Directory: objectStorePath,
	})
	require.NoError(t, err)
	testDeleteRequestsStore, err := NewDeleteStore(workingDir, storage.NewIndexStorageClient(objectClient, ""))
	require.NoError(t, err)

	defer testDeleteRequestsStore.Stop()

	// add requests for both the users to the store
	for i := 0; i < len(user1ExpectedRequests); i++ {
		requestID, err := testDeleteRequestsStore.(*deleteRequestsStore).addDeleteRequest(
			context.Background(),
			user1ExpectedRequests[i].UserID,
			user1ExpectedRequests[i].CreatedAt,
			user1ExpectedRequests[i].StartTime,
			user1ExpectedRequests[i].EndTime,
			user1ExpectedRequests[i].Query,
		)
		require.NoError(t, err)
		user1ExpectedRequests[i].RequestID = string(requestID)
		require.NoError(t, user1ExpectedRequests[i].SetQuery(user1ExpectedRequests[i].Query))

		requestID, err = testDeleteRequestsStore.(*deleteRequestsStore).addDeleteRequest(
			context.Background(),
			user2ExpectedRequests[i].UserID,
			user2ExpectedRequests[i].CreatedAt,
			user2ExpectedRequests[i].StartTime,
			user2ExpectedRequests[i].EndTime,
			user2ExpectedRequests[i].Query,
		)
		require.NoError(t, err)
		user2ExpectedRequests[i].RequestID = string(requestID)
		require.NoError(t, user2ExpectedRequests[i].SetQuery(user2ExpectedRequests[i].Query))
	}

	// get all requests with StatusReceived and see if they have expected values
	deleteRequests, err := testDeleteRequestsStore.GetDeleteRequestsByStatus(context.Background(), StatusReceived)
	require.NoError(t, err)
	compareRequests(t, append(user1ExpectedRequests, user2ExpectedRequests...), deleteRequests)

	// get user specific requests and see if they have expected values
	user1Requests, err := testDeleteRequestsStore.GetAllDeleteRequestsForUser(context.Background(), user1)
	require.NoError(t, err)
	compareRequests(t, user1ExpectedRequests, user1Requests)

	user2Requests, err := testDeleteRequestsStore.GetAllDeleteRequestsForUser(context.Background(), user2)
	require.NoError(t, err)
	compareRequests(t, user2ExpectedRequests, user2Requests)

	createGenNumber, err := testDeleteRequestsStore.GetCacheGenerationNumber(context.Background(), user1)
	require.NoError(t, err)
	require.NotEmpty(t, createGenNumber)

	createGenNumber2, err := testDeleteRequestsStore.GetCacheGenerationNumber(context.Background(), user2)
	require.NoError(t, err)
	require.NotEmpty(t, createGenNumber2)

	// get individual delete requests by id and see if they have expected values
	for _, expectedRequest := range append(user1Requests, user2Requests...) {
		actualRequest, err := testDeleteRequestsStore.GetDeleteRequest(context.Background(), expectedRequest.UserID, expectedRequest.RequestID)
		require.NoError(t, err)
		require.Equal(t, expectedRequest, *actualRequest)
	}

	// try a non-existent request and see if it throws ErrDeleteRequestNotFound
	_, err = testDeleteRequestsStore.GetDeleteRequest(context.Background(), "user3", "na")
	require.ErrorIs(t, err, ErrDeleteRequestNotFound)

	// update some of the delete requests for both the users to processed
	for i := 0; i < len(user1ExpectedRequests); i++ {
		var request DeleteRequest
		if i%2 != 0 {
			user1ExpectedRequests[i].Status = StatusProcessed
			request = user1ExpectedRequests[i]
		} else {
			user2ExpectedRequests[i].Status = StatusProcessed
			request = user2ExpectedRequests[i]
		}

		require.NoError(t, testDeleteRequestsStore.UpdateStatus(context.Background(), request.UserID, request.RequestID, StatusProcessed))
	}

	// see if requests in the store have right values
	user1Requests, err = testDeleteRequestsStore.GetAllDeleteRequestsForUser(context.Background(), user1)
	require.NoError(t, err)
	compareRequests(t, user1ExpectedRequests, user1Requests)

	user2Requests, err = testDeleteRequestsStore.GetAllDeleteRequestsForUser(context.Background(), user2)
	require.NoError(t, err)
	compareRequests(t, user2ExpectedRequests, user2Requests)

	updateGenNumber, err := testDeleteRequestsStore.GetCacheGenerationNumber(context.Background(), user1)
	require.NoError(t, err)
	require.NotEqual(t, createGenNumber, updateGenNumber)

	updateGenNumber2, err := testDeleteRequestsStore.GetCacheGenerationNumber(context.Background(), user2)
	require.NoError(t, err)
	require.NotEqual(t, createGenNumber2, updateGenNumber2)

	// delete the requests from the store updated previously
	var remainingRequests []DeleteRequest
	for i := 0; i < len(user1ExpectedRequests); i++ {
		var request DeleteRequest
		if i%2 != 0 {
			user1ExpectedRequests[i].Status = StatusProcessed
			request = user1ExpectedRequests[i]
			remainingRequests = append(remainingRequests, user2ExpectedRequests[i])
		} else {
			user2ExpectedRequests[i].Status = StatusProcessed
			request = user2ExpectedRequests[i]
			remainingRequests = append(remainingRequests, user1ExpectedRequests[i])
		}

		require.NoError(t, testDeleteRequestsStore.RemoveDeleteRequest(context.Background(), request.UserID, request.RequestID, request.CreatedAt, request.StartTime, request.EndTime))
	}

	// see if the store has the right remaining requests
	deleteRequests, err = testDeleteRequestsStore.GetDeleteRequestsByStatus(context.Background(), StatusReceived)
	require.NoError(t, err)
	compareRequests(t, remainingRequests, deleteRequests)

	deleteGenNumber, err := testDeleteRequestsStore.GetCacheGenerationNumber(context.Background(), user1)
	require.NoError(t, err)
	require.NotEqual(t, updateGenNumber, deleteGenNumber)

	deleteGenNumber2, err := testDeleteRequestsStore.GetCacheGenerationNumber(context.Background(), user2)
	require.NoError(t, err)
	require.NotEqual(t, updateGenNumber2, deleteGenNumber2)
}

func compareRequests(t *testing.T, expected []DeleteRequest, actual []DeleteRequest) {
	require.Len(t, actual, len(expected))
	sort.Slice(expected, func(i, j int) bool {
		return expected[i].RequestID < expected[j].RequestID
	})
	sort.Slice(actual, func(i, j int) bool {
		return actual[i].RequestID < actual[j].RequestID
	})
	for i, deleteRequest := range actual {
		require.Equal(t, expected[i], deleteRequest)
	}
}
