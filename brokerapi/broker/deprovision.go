package broker

import (
	"context"
	"fmt"

	"code.cloudfoundry.org/lager"
	"github.com/cloudfoundry/cloud-service-broker/dbservice/models"
	"github.com/cloudfoundry/cloud-service-broker/internal/paramparser"
	"github.com/cloudfoundry/cloud-service-broker/utils/correlation"
	"github.com/cloudfoundry/cloud-service-broker/utils/request"
	"github.com/pivotal-cf/brokerapi/v8/domain"
	"github.com/pivotal-cf/brokerapi/v8/domain/apiresponses"
)

// Deprovision destroys an existing instance of a service.
// It is bound to the `DELETE /v2/service_instances/:instance_id` endpoint and can be called using the `cf delete-service` command.
// If a deprovision is asynchronous, the returned DeprovisionServiceSpec will contain the operation ID for tracking its progress.
func (broker *ServiceBroker) Deprovision(ctx context.Context, instanceID string, details domain.DeprovisionDetails, clientSupportsAsync bool) (response domain.DeprovisionServiceSpec, err error) {
	broker.Logger.Info("Deprovisioning", correlation.ID(ctx), lager.Data{
		"instance_id":        instanceID,
		"accepts_incomplete": clientSupportsAsync,
		"details":            details,
	})

	if !clientSupportsAsync {
		return response, apiresponses.ErrAsyncRequired
	}

	// make sure that instance actually exists
	exists, err := broker.store.ExistsServiceInstanceDetails(instanceID)
	switch {
	case err != nil:
		return response, fmt.Errorf("database error checking for existing instance: %s", err)
	case !exists:
		return response, apiresponses.ErrInstanceDoesNotExist
	}

	instance, err := broker.store.GetServiceInstanceDetails(instanceID)
	if err != nil {
		return response, fmt.Errorf("database error getting existing instance: %s", err)
	}

	serviceDefinition, serviceProvider, err := broker.getDefinitionAndProvider(instance.ServiceGUID)
	if err != nil {
		return response, err
	}

	err = serviceProvider.CheckUpgradeAvailable(generateTFInstanceID(instanceID))
	if err != nil {
		return response, fmt.Errorf("failed to delete: %s", err.Error())
	}

	// verify the service exists and the plan exists
	plan, err := serviceDefinition.GetPlanByID(details.PlanID)
	if err != nil {
		return response, err
	}

	parameters, err := broker.store.GetProvisionRequestDetails(instanceID)
	if err != nil {
		return response, fmt.Errorf("error retrieving provision request details for %q: %w", instanceID, err)
	}

	provisionDetails := paramparser.ProvisionDetails{
		ServiceID:     details.ServiceID,
		PlanID:        details.PlanID,
		RequestParams: parameters,
	}

	// validate parameters meet the service's schema and merge the user vars with
	// the plan's
	vars, err := serviceDefinition.ProvisionVariables(instanceID, provisionDetails, *plan, request.DecodeOriginatingIdentityHeader(ctx))
	if err != nil {
		return response, err
	}

	operationID, err := serviceProvider.Deprovision(ctx, instance.GUID, details, vars)
	if err != nil {
		return response, err
	}

	if operationID == nil {
		// soft-delete instance details from the db if this is a synchronous operation
		// if it's an async operation we can't delete from the db until we're sure delete succeeded, so this is
		// handled internally to LastOperation
		if err := broker.store.DeleteServiceInstanceDetails(instanceID); err != nil {
			return response, fmt.Errorf("error deleting instance details from database: %s. WARNING: this instance will remain visible in cf. Contact your operator for cleanup", err)
		}
		if err := broker.store.DeleteProvisionRequestDetails(instanceID); err != nil {
			return response, fmt.Errorf("error deleting provision request details from the database: %w", err)
		}
		return response, nil
	}

	response.IsAsync = true
	response.OperationData = *operationID

	instance.OperationType = models.DeprovisionOperationType
	instance.OperationGUID = *operationID
	if err := broker.store.StoreServiceInstanceDetails(instance); err != nil {
		return response, fmt.Errorf("error saving instance details to database: %s. WARNING: this instance will remain visible in cf. Contact your operator for cleanup", err)
	}
	return response, nil
}
