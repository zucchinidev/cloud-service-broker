package invoker

import (
	"context"

	"github.com/cloudfoundry/cloud-service-broker/pkg/providers/tf/executor"
	"github.com/cloudfoundry/cloud-service-broker/pkg/providers/tf/workspace"

	"github.com/cloudfoundry/cloud-service-broker/pkg/providers/tf/command"
)

func NewTerraform012Invoker(executor executor.TerraformExecutor, pluginDirectory string) TerraformInvoker {
	return Terraform012Invoker{executor: executor, pluginDirectory: pluginDirectory}
}

type Terraform012Invoker struct {
	executor        executor.TerraformExecutor
	pluginDirectory string
}

func (cmd Terraform012Invoker) Apply(ctx context.Context, workspace workspace.Workspace) error {
	_, err := workspace.Execute(ctx, cmd.executor,
		command.NewInit012(cmd.pluginDirectory),
		command.NewApply())
	return err
}

func (cmd Terraform012Invoker) Show(ctx context.Context, workspace workspace.Workspace) (string, error) {
	output, err := workspace.Execute(ctx, cmd.executor,
		command.NewInit012(cmd.pluginDirectory),
		command.NewShow())
	return output.StdOut, err
}

func (cmd Terraform012Invoker) Destroy(ctx context.Context, workspace workspace.Workspace) error {
	_, err := workspace.Execute(ctx, cmd.executor,
		command.NewInit012(cmd.pluginDirectory),
		command.NewDestroy())
	return err
}

func (cmd Terraform012Invoker) Plan(ctx context.Context, workspace workspace.Workspace) (executor.ExecutionOutput, error) {
	return workspace.Execute(ctx, cmd.executor,
		command.NewInit012(cmd.pluginDirectory),
		command.NewPlan())
}

func (cmd Terraform012Invoker) Import(ctx context.Context, workspace workspace.Workspace, resources map[string]string) error {
	commands := []command.TerraformCommand{
		command.NewInit012(cmd.pluginDirectory),
	}
	for resource, id := range resources {
		commands = append(commands, command.NewImport(resource, id))
	}

	_, err := workspace.Execute(ctx, cmd.executor, commands...)
	return err
}
