# Claude Development Guide

This document contains important information for future development tasks on the DragonOps Orchestrator codebase.

## Architecture Overview

### Core Components

- **cmd/**: CLI command definitions and entry points
- **internal/cmdRunners/**: Core business logic for different operation types
  - `app/`: Application deployment operations
  - `group/`: Group-level infrastructure operations  
  - `observability/`: Observability stack management
  - `plan/`: Planning and analysis operations
- **internal/terraform/**: Terraform execution and plan analysis
- **internal/utils/**: Shared utilities and helper functions

### Key Technologies

- **Terraform Integration**: Uses `hashicorp/terraform-exec` for Terraform operations
- **AWS SDK**: `aws-sdk-go-v2` for AWS service interactions
- **ORM**: `magicmodel-go` for DynamoDB operations
- **Logging**: `zerolog` for structured logging

## ECS Deployment Status Logic

### Implementation Location
- **Main Logic**: `internal/cmdRunners/app/apply.go` in `formatWithWorkerAndApply()`
- **Helper Functions**: `internal/terraform/plan.go` 
  - `CheckForECSServiceChanges()`: Runs terraform plan and analyzes output
  - `hasECSResourceChanges()`: Parses plan for ECS resource changes

### Decision Flow
1. **Serverless Apps**: Always mark as SUCCEEDED immediately
2. **ECS Apps**: 
   - Run terraform plan before marking deployment status
   - If ECS service/task definition changes detected → Leave status for lambda
   - If no ECS changes detected → Mark as SUCCEEDED
   - If plan analysis fails → Default to SUCCEEDED (fail-safe)

### ECS Resource Types Monitored
- `aws_ecs_service`: ECS service definitions
- `aws_ecs_task_definition`: Task definition changes

### Actions That Trigger ECS Deployments
- `create`: New resource creation
- `update`: Resource updates
- `replace`: Resource replacement

### Actions That Don't Trigger Deployments
- `delete`: Resource deletion
- `no-op`: No changes

## Development Patterns

### Error Handling
- Use structured logging with `zerolog`
- Include contextual information (AppID, deployment ID, etc.)
- Follow fail-safe patterns (default to success when uncertain)
- Return errors with descriptive messages

### Terraform Operations
- Always use `terraform.NewTerraform()` for initialization
- Handle role assumption with `BackendConfig` for cross-account scenarios
- Clean up temporary files (plan files, etc.)
- Use structured data (`tfjson.Plan`) instead of raw text parsing

### Database Operations
- Use `magicmodel.Operator` for DynamoDB operations
- Always check for `o.Err` after operations
- Use `WhereV4()` for complex queries
- Update deployment status using `utils.UpdateDeploymentStatus()`

### Testing
- Place tests in `*_test.go` files alongside source code
- Use table-driven tests for multiple scenarios
- Test both success and error cases
- Mock external dependencies when possible

## Common Development Tasks

### Adding New Resource Type Detection
1. Add resource type to `ecsResourceTypes` map in `hasECSResourceChanges()`
2. Consider if the resource type actually triggers deployments
3. Add test cases in `plan_test.go`

### Modifying Deployment Status Logic
1. Update logic in `formatWithWorkerAndApply()` function
2. Ensure error handling follows existing patterns
3. Add appropriate logging statements
4. Test with different app configurations

### Adding New Terraform Operations
1. Add function to `internal/terraform/` package
2. Follow existing patterns for initialization and cleanup
3. Handle role assumption if needed
4. Add appropriate error handling and logging

## Configuration and Environment

### Environment Variables
- `IS_LOCAL`: Set to "true" for local development
- `DRAGONOPS_TERRAFORM_ARTIFACT`: Path to terraform templates
- `DRAGONOPS_TERRAFORM_DESTINATION`: Destination for terraform files
- `DRAGONOPS_API`: API endpoint for DragonOps services

### Local Development Setup
- Copy worker templates and binary to `app/` directory
- Set AWS profile and region appropriately
- Use test deployment IDs and app configurations
- Reference README.md for complete setup instructions

## Troubleshooting

### Common Issues
1. **Terraform Plan Failures**: Check role permissions and backend configuration
2. **ECS Detection False Positives**: Verify resource type matching logic
3. **Database Update Failures**: Check magicmodel operations and permissions
4. **Cross-Account Issues**: Verify role assumption configuration

### Debugging
- Use structured logging to trace execution flow
- Check DynamoDB for deployment status updates
- Verify terraform plan output format
- Test with minimal reproduction cases

## Future Enhancement Opportunities

### Potential Improvements
1. **Enhanced ECS Detection**: 
   - Detect changes that affect specific service attributes
   - Support for additional ECS resource types
   - Blue/green deployment detection

2. **Performance Optimizations**:
   - Cache terraform plan results
   - Parallel processing for multiple environments
   - Optimized DynamoDB queries

3. **Monitoring and Observability**:
   - Metrics for deployment success rates
   - Alerting for unexpected failures
   - Performance tracking for terraform operations

4. **Testing Enhancements**:
   - Integration tests with real terraform plans
   - End-to-end testing with mock AWS services
   - Property-based testing for edge cases

### Breaking Changes to Consider
- Changes to the `formatWithWorkerAndApply()` function signature
- Modifications to deployment status enum values
- Updates to terraform plan analysis logic
- Changes to database schema or operations

Remember to update this document when making significant architectural changes or adding new patterns to the codebase.