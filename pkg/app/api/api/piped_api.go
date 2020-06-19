// Copyright 2020 The PipeCD Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pipe-cd/pipe/pkg/app/api/applicationlivestatestore"
	"github.com/pipe-cd/pipe/pkg/app/api/service/pipedservice"
	"github.com/pipe-cd/pipe/pkg/app/api/stagelogstore"
	"github.com/pipe-cd/pipe/pkg/datastore"
	"github.com/pipe-cd/pipe/pkg/model"
	"github.com/pipe-cd/pipe/pkg/rpc/rpcauth"
)

// PipedAPI implements the behaviors for the gRPC definitions of PipedAPI.
type PipedAPI struct {
	applicationStore          datastore.ApplicationStore
	commandStore              datastore.CommandStore
	deploymentStore           datastore.DeploymentStore
	environmentStore          datastore.EnvironmentStore
	pipedStatsStore           datastore.PipedStatsStore
	pipedStore                datastore.PipedStore
	projectStore              datastore.ProjectStore
	stageLogStore             stagelogstore.Store
	applicationLiveStateStore applicationlivestatestore.Store

	logger *zap.Logger
}

// NewPipedAPI creates a new PipedAPI instance.
func NewPipedAPI(ds datastore.DataStore, sls stagelogstore.Store, alss applicationlivestatestore.Store, logger *zap.Logger) *PipedAPI {
	a := &PipedAPI{
		applicationStore:          datastore.NewApplicationStore(ds),
		commandStore:              datastore.NewCommandStore(ds),
		deploymentStore:           datastore.NewDeploymentStore(ds),
		environmentStore:          datastore.NewEnvironmentStore(ds),
		pipedStatsStore:           datastore.NewPipedStatsStore(ds),
		pipedStore:                datastore.NewPipedStore(ds),
		projectStore:              datastore.NewProjectStore(ds),
		stageLogStore:             sls,
		applicationLiveStateStore: alss,
		logger:                    logger.Named("piped-api"),
	}
	return a
}

// Register registers all handling of this service into the specified gRPC server.
func (a *PipedAPI) Register(server *grpc.Server) {
	pipedservice.RegisterPipedServiceServer(server, a)
}

// Ping is periodically sent by piped to report its status/stats to API.
// The received stats will be written to the cache immediately.
// The cache data may be lost anytime so we need a singleton Persister
// to persist those data into datastore every n minutes.
func (a *PipedAPI) Ping(ctx context.Context, req *pipedservice.PingRequest) (*pipedservice.PingResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// ReportPipedMeta is sent by piped while starting up to report its metadata
// such as configured cloud providers.
func (a *PipedAPI) ReportPipedMeta(ctx context.Context, req *pipedservice.ReportPipedMetaRequest) (*pipedservice.ReportPipedMetaResponse, error) {
	_, pipedID, _, err := rpcauth.ExtractPipedToken(ctx)
	if err != nil {
		return nil, err
	}

	if err = a.pipedStore.UpdatePiped(ctx, pipedID, datastore.PipedMetadataUpdater(req.CloudProviders, req.RepositoryIds, req.Version)); err != nil {
		switch err {
		case datastore.ErrNotFound:
			return nil, status.Error(codes.InvalidArgument, "piped is not found")
		case datastore.ErrInvalidArgument:
			return nil, status.Error(codes.InvalidArgument, "invalid value for update")
		default:
			a.logger.Error("failed to update the piped metadata",
				zap.String("piped-id", pipedID),
				zap.Error(err),
			)
			return nil, status.Error(codes.Internal, "failed to update the piped metadata")
		}
	}
	return &pipedservice.ReportPipedMetaResponse{}, nil
}

// ListApplications returns a list of registered applications
// that should be managed by the requested piped.
// Disabled applications should not be included in the response.
// Piped uses this RPC to fetch and sync the application configuration into its local database.
func (a *PipedAPI) ListApplications(ctx context.Context, req *pipedservice.ListApplicationsRequest) (*pipedservice.ListApplicationsResponse, error) {
	projectID, pipedID, _, err := rpcauth.ExtractPipedToken(ctx)
	if err != nil {
		return nil, err
	}
	opts := datastore.ListOptions{
		Filters: []datastore.ListFilter{
			{
				Field:    "ProjectId",
				Operator: "==",
				Value:    projectID,
			},
			{
				Field:    "PipedId",
				Operator: "==",
				Value:    pipedID,
			},
			{
				Field:    "Disabled",
				Operator: "==",
				Value:    false,
			},
		},
	}
	// TOOD: Support pagination in ListApplications
	apps, err := a.applicationStore.ListApplications(ctx, opts)
	if err != nil {
		a.logger.Error("failed to fetch applications", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to fetch applications")
	}
	return &pipedservice.ListApplicationsResponse{
		Applications: apps,
	}, nil
}

// ReportApplicationSyncState is used to update the sync status of an application.
func (a *PipedAPI) ReportApplicationSyncState(ctx context.Context, req *pipedservice.ReportApplicationSyncStateRequest) (*pipedservice.ReportApplicationSyncStateResponse, error) {
	err := a.applicationStore.PutApplicationSyncState(ctx, req.ApplicationId, req.State)
	if err != nil {
		switch err {
		case datastore.ErrNotFound:
			return nil, status.Error(codes.InvalidArgument, "application is not found")
		case datastore.ErrInvalidArgument:
			return nil, status.Error(codes.InvalidArgument, "invalid value for update")
		default:
			a.logger.Error("failed to update application sync state",
				zap.String("application-id", req.ApplicationId),
				zap.Error(err),
			)
			return nil, status.Error(codes.Internal, "failed to update the application sync state")
		}
	}
	return &pipedservice.ReportApplicationSyncStateResponse{}, nil
}

// ReportApplicationMostRecentDeployment is used to update the basic information about
// the most recent deployment of a specific application.
func (a *PipedAPI) ReportApplicationMostRecentDeployment(ctx context.Context, req *pipedservice.ReportApplicationMostRecentDeploymentRequest) (*pipedservice.ReportApplicationMostRecentDeploymentResponse, error) {
	err := a.applicationStore.PutApplicationMostRecentDeployment(ctx, req.ApplicationId, req.Status, req.Deployment)
	if err != nil {
		switch err {
		case datastore.ErrNotFound:
			return nil, status.Error(codes.InvalidArgument, "application is not found")
		case datastore.ErrInvalidArgument:
			return nil, status.Error(codes.InvalidArgument, "invalid value for update")
		default:
			a.logger.Error("failed to update application completed deployment",
				zap.String("application-id", req.ApplicationId),
				zap.Error(err),
			)
			return nil, status.Error(codes.Internal, "failed to update the application completed deployment")
		}
	}
	return &pipedservice.ReportApplicationMostRecentDeploymentResponse{}, nil
}

// ListNotCompletedDeployments returns a list of not completed deployments
// which are managed by this piped.
// DeploymentController component uses this RPC to spawns/syncs its local deployment executors.
func (a *PipedAPI) ListNotCompletedDeployments(ctx context.Context, req *pipedservice.ListNotCompletedDeploymentsRequest) (*pipedservice.ListNotCompletedDeploymentsResponse, error) {
	_, pipedID, _, err := rpcauth.ExtractPipedToken(ctx)
	if err != nil {
		return nil, err
	}

	opts := datastore.ListOptions{
		Filters: []datastore.ListFilter{
			{
				Field:    "PipedId",
				Operator: "==",
				Value:    pipedID,
			},
			// TODO: Change to simple conditional clause without using OR clause for portability
			// Note: firestore does not support OR operator.
			// See more: https://firebase.google.com/docs/firestore/query-data/queries?hl=en
			{
				Field:    "Status",
				Operator: "in",
				Value:    model.GetNotCompletedDeploymentStatuses(),
			},
		},
	}

	// TODO: Support pagination in ListNotCompletedDeployments
	deployments, err := a.deploymentStore.ListDeployments(ctx, opts)
	if err != nil {
		a.logger.Error("failed to fetch deployments", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to fetch deployments")
	}
	return &pipedservice.ListNotCompletedDeploymentsResponse{
		Deployments: deployments,
	}, nil
}

// GetMostRecentDeployment returns the most recent deployment of the given application.
func (a *PipedAPI) GetMostRecentDeployment(ctx context.Context, req *pipedservice.GetMostRecentDeploymentRequest) (*pipedservice.GetMostRecentDeploymentResponse, error) {
	// TODO: Does not use composite indexes, and register new collection for most recent deployment
	// Note: We need to manage two composite indexes:
	// 1. ApplicationId:ASC, CreatedAt: DESC
	// 2. ApplicationId:ASC, Status:ASC, CreatedAt: DESC
	filters := []datastore.ListFilter{
		{
			Field:    "ApplicationId",
			Operator: "==",
			Value:    req.ApplicationId,
		},
	}
	if req.Status != nil {
		filters = append(filters, datastore.ListFilter{
			Field:    "Status",
			Operator: "==",
			Value:    req.Status.Value,
		})
	}
	// Sort to get the most recent data
	opts := datastore.ListOptions{
		Filters: filters,
		Orders: []datastore.Order{
			{
				Field:     "CreatedAt",
				Direction: datastore.Desc,
			},
		},
		Page:     0, // Note: This parameter requires in other datastores, but in Firestore it is enough setting only PageSize.
		PageSize: 1,
	}

	deployments, err := a.deploymentStore.ListDeployments(ctx, opts)
	if err != nil {
		a.logger.Error("failed to fetch deployments", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to fetch deployments")
	}
	if len(deployments) == 0 {
		return nil, status.Error(codes.NotFound, "deployment is not found")
	}

	return &pipedservice.GetMostRecentDeploymentResponse{
		Deployment: deployments[0],
	}, nil
}

// CreateDeployment creates/triggers a new deployment for an application
// that is managed by this piped.
// This will be used by DeploymentTrigger component.
func (a *PipedAPI) CreateDeployment(ctx context.Context, req *pipedservice.CreateDeploymentRequest) (*pipedservice.CreateDeploymentResponse, error) {
	err := a.deploymentStore.AddDeployment(ctx, req.Deployment)
	if errors.Is(err, datastore.ErrAlreadyExists) {
		return nil, status.Error(codes.AlreadyExists, "deployment already exists")
	}
	if err != nil {
		a.logger.Error("failed to create deployment", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to create deployment")
	}
	return &pipedservice.CreateDeploymentResponse{}, nil
}

// ReportDeploymentPlanned used by piped to update the status
// of a specific deployment to PLANNED.
func (a *PipedAPI) ReportDeploymentPlanned(ctx context.Context, req *pipedservice.ReportDeploymentPlannedRequest) (*pipedservice.ReportDeploymentPlannedResponse, error) {
	updater := datastore.DeploymentToPlannedUpdater(req.Description, req.StatusDescription, req.Stages)
	err := a.deploymentStore.PutDeployment(ctx, req.DeploymentId, updater)
	if err != nil {
		switch err {
		case datastore.ErrNotFound:
			return nil, status.Error(codes.InvalidArgument, "deployment is not found")
		case datastore.ErrInvalidArgument:
			return nil, status.Error(codes.InvalidArgument, "invalid value for update")
		default:
			a.logger.Error("failed to update deployment to be planned",
				zap.String("deployment-id", req.DeploymentId),
				zap.Error(err),
			)
			return nil, status.Error(codes.Internal, "failed to update deployment to be planned")
		}
	}
	return &pipedservice.ReportDeploymentPlannedResponse{}, nil
}

// ReportDeploymentStatusChanged is used to update the status
// of a specific deployment to RUNNING or ROLLING_BACK.
func (a *PipedAPI) ReportDeploymentStatusChanged(ctx context.Context, req *pipedservice.ReportDeploymentStatusChangedRequest) (*pipedservice.ReportDeploymentStatusChangedResponse, error) {
	updater := datastore.DeploymentStatusUpdater(req.Status, req.StatusDescription)
	err := a.deploymentStore.PutDeployment(ctx, req.DeploymentId, updater)
	if err != nil {
		switch err {
		case datastore.ErrNotFound:
			return nil, status.Error(codes.InvalidArgument, "deployment is not found")
		case datastore.ErrInvalidArgument:
			return nil, status.Error(codes.InvalidArgument, "invalid value for update")
		default:
			a.logger.Error("failed to update deployment status",
				zap.String("deployment-id", req.DeploymentId),
				zap.Error(err),
			)
			return nil, status.Error(codes.Internal, "failed to update deployment status")
		}
	}
	return &pipedservice.ReportDeploymentStatusChangedResponse{}, nil
}

// ReportDeploymentCompleted used by piped to update the status
// of a specific deployment to SUCCESS | FAILURE | CANCELLED.
func (a *PipedAPI) ReportDeploymentCompleted(ctx context.Context, req *pipedservice.ReportDeploymentCompletedRequest) (*pipedservice.ReportDeploymentCompletedResponse, error) {
	updater := datastore.DeploymentToCompletedUpdater(req.Status, req.StageStatuses, req.StatusDescription, req.CompletedAt)
	err := a.deploymentStore.PutDeployment(ctx, req.DeploymentId, updater)
	if err != nil {
		switch err {
		case datastore.ErrNotFound:
			return nil, status.Error(codes.InvalidArgument, "deployment is not found")
		case datastore.ErrInvalidArgument:
			return nil, status.Error(codes.InvalidArgument, "invalid value for update")
		default:
			a.logger.Error("failed to update deployment to be completed",
				zap.String("deployment-id", req.DeploymentId),
				zap.Error(err),
			)
			return nil, status.Error(codes.Internal, "failed to update deployment to be completed")
		}
	}
	return &pipedservice.ReportDeploymentCompletedResponse{}, nil
}

// SaveDeploymentMetadata used by piped to persist the metadata of a specific deployment.
func (a *PipedAPI) SaveDeploymentMetadata(ctx context.Context, req *pipedservice.SaveDeploymentMetadataRequest) (*pipedservice.SaveDeploymentMetadataResponse, error) {
	err := a.deploymentStore.PutDeploymentMetadata(ctx, req.DeploymentId, req.Metadata)
	if errors.Is(err, datastore.ErrNotFound) {
		return nil, status.Error(codes.InvalidArgument, "deployment is not found")
	}
	if err != nil {
		a.logger.Error("failed to save deployment metadata",
			zap.String("deployment-id", req.DeploymentId),
			zap.Error(err),
		)
		return nil, status.Error(codes.Internal, "failed to save deployment metadata")
	}
	return &pipedservice.SaveDeploymentMetadataResponse{}, nil
}

// SaveStageMetadata used by piped to persist the metadata
// of a specific stage of a deployment.
func (a *PipedAPI) SaveStageMetadata(ctx context.Context, req *pipedservice.SaveStageMetadataRequest) (*pipedservice.SaveStageMetadataResponse, error) {
	err := a.deploymentStore.PutDeploymentStageMetadata(ctx, req.DeploymentId, req.StageId, req.Metadata)
	if err != nil {
		switch errors.Unwrap(err) {
		case datastore.ErrNotFound:
			return nil, status.Error(codes.InvalidArgument, "deployment is not found")
		case datastore.ErrInvalidArgument:
			return nil, status.Error(codes.InvalidArgument, "invalid value for update")
		default:
			a.logger.Error("failed to save deployment stage metadata",
				zap.String("deployment-id", req.DeploymentId),
				zap.String("stage-id", req.StageId),
				zap.Error(err),
			)
			return nil, status.Error(codes.Internal, "failed to save deployment stage metadata")
		}
	}
	return &pipedservice.SaveStageMetadataResponse{}, nil
}

// ReportStageLogs is sent by piped to save the log of a pipeline stage.
func (a *PipedAPI) ReportStageLogs(ctx context.Context, req *pipedservice.ReportStageLogsRequest) (*pipedservice.ReportStageLogsResponse, error) {
	err := a.stageLogStore.AppendLogs(ctx, req.DeploymentId, req.StageId, req.RetriedCount, req.Blocks)
	if errors.Is(err, stagelogstore.ErrAlreadyCompleted) {
		return nil, status.Error(codes.FailedPrecondition, "could not append the logs because the stage was already completed")
	}
	if err != nil {
		a.logger.Error("failed to append logs", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to append logs")
	}
	return &pipedservice.ReportStageLogsResponse{}, nil
}

// ReportStageLogsFromLastCheckpoint is used to save the full logs from the most recently saved point.
func (a *PipedAPI) ReportStageLogsFromLastCheckpoint(ctx context.Context, req *pipedservice.ReportStageLogsFromLastCheckpointRequest) (*pipedservice.ReportStageLogsFromLastCheckpointResponse, error) {
	err := a.stageLogStore.AppendLogsFromLastCheckpoint(ctx, req.DeploymentId, req.StageId, req.RetriedCount, req.Blocks, req.Completed)
	if errors.Is(err, stagelogstore.ErrAlreadyCompleted) {
		return nil, status.Error(codes.FailedPrecondition, "could not append the logs because the stage was already completed")
	}
	if err != nil {
		a.logger.Error("failed to append logs", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to append logs")
	}
	return &pipedservice.ReportStageLogsFromLastCheckpointResponse{}, nil
}

// ReportStageStatusChanged used by piped to update the status
// of a specific stage of a deployment.
func (a *PipedAPI) ReportStageStatusChanged(ctx context.Context, req *pipedservice.ReportStageStatusChangedRequest) (*pipedservice.ReportStageStatusChangedResponse, error) {
	updater := datastore.StageStatusChangedUpdater(req.StageId, req.Status, req.StatusDescription, req.RetriedCount, req.CompletedAt)
	err := a.deploymentStore.PutDeployment(ctx, req.DeploymentId, updater)
	if err != nil {
		switch err {
		case datastore.ErrNotFound:
			return nil, status.Error(codes.InvalidArgument, "deployment is not found")
		case datastore.ErrInvalidArgument:
			return nil, status.Error(codes.InvalidArgument, "invalid value for update")
		default:
			a.logger.Error("failed to update stage status",
				zap.String("deployment-id", req.DeploymentId),
				zap.String("stage-id", req.StageId),
				zap.Error(err),
			)
			return nil, status.Error(codes.Internal, "failed to update stage status")
		}
	}
	return &pipedservice.ReportStageStatusChangedResponse{}, nil
}

// ListUnhandledCommands is periodically called by piped to obtain the commands
// that should be handled.
// Whenever an user makes an interaction from WebUI (cancel/approve/retry/sync)
// a new command with a unique identifier will be generated an saved into the datastore.
// Piped uses this RPC to list all still-not-handled commands to handle them,
// then report back the result to server.
// On other side, the web will periodically check the command status and feedback the result to user.
// In the future, we may need a solution to remove all old-handled commands from datastore for space.
func (a *PipedAPI) ListUnhandledCommands(ctx context.Context, req *pipedservice.ListUnhandledCommandsRequest) (*pipedservice.ListUnhandledCommandsResponse, error) {
	return &pipedservice.ListUnhandledCommandsResponse{}, nil
	// nil, status.Error(codes.Unimplemented, "")
}

// ReportCommandHandled is called by piped to mark a specific command as handled.
// The request payload will contain the handle status as well as any additional result data.
// The handle result should be updated to both datastore and cache (for reading from web).
func (a *PipedAPI) ReportCommandHandled(ctx context.Context, req *pipedservice.ReportCommandHandledRequest) (*pipedservice.ReportCommandHandledResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// ReportApplicationLiveState is periodically sent to correct full state of an application.
// For kubernetes application, this contains a full tree of its kubernetes resources.
// The tree data should be written into filestore immediately and then the state in cache should be refreshsed too.
func (a *PipedAPI) ReportApplicationLiveState(ctx context.Context, req *pipedservice.ReportApplicationLiveStateRequest) (*pipedservice.ReportApplicationLiveStateResponse, error) {
	if err := a.applicationLiveStateStore.PutStateSnapshot(ctx, req.Snapshot); err != nil {
		return nil, status.Error(codes.Internal, "failed to report application live state")
	}
	return &pipedservice.ReportApplicationLiveStateResponse{}, nil
}

// ReportApplicationLiveStateEvents is sent by piped to submit one or multiple events
// about the changes of application state.
// Control plane uses the received events to update the state of application-resource-tree.
// We want to start by a simple solution at this initial stage of development,
// so the API server just handles as below:
// - loads the releated application-resource-tree from filestore
// - checks and builds new state for the application-resource-tree
// - updates new state into fielstore and cache (cache data is for reading while handling web requests)
// In the future, we may want to redesign the behavior of this RPC by using pubsub/queue pattern.
// After receiving the events, all of them will be publish into a queue immediately,
// and then another Handler service will pick them inorder to apply to build new state.
// By that way we can control the traffic to the datastore in a better way.
func (a *PipedAPI) ReportApplicationLiveStateEvents(ctx context.Context, req *pipedservice.ReportApplicationLiveStateEventsRequest) (*pipedservice.ReportApplicationLiveStateEventsResponse, error) {
	a.applicationLiveStateStore.PatchKubernetesApplicationLiveState(ctx, req.KubernetesEvents)
	// TODO: Patch Terraform application live state
	// TODO: Patch Cloud Run application live state
	// TODO: Patch Lambda application live state
	return &pipedservice.ReportApplicationLiveStateEventsResponse{}, nil
}
