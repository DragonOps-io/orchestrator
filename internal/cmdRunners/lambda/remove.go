package lambda

//func Remove(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
//	log.Debug().
//		Str("AppID", payload.AppID).
//		Msg("Attempting to remove app")
//
//	app := types.App{}
//	o := mm.Find(&app, payload.AppID)
//	if o.Err != nil {
//		log.Err(o.Err).Str("AppID", payload.AppID).Msg("Error finding app")
//		return fmt.Errorf("an error occurred when trying to find the item with id %s: %s", payload.AppID, o.Err)
//	}
//	log.Debug().Str("AppID", app.ID).Msg("Found app")
//
//	var appEnvironmentsToDestroy []types.Environment
//	for _, envId := range payload.EnvironmentIDs {
//		env := types.Environment{}
//		o = mm.Find(&env, envId)
//		if o.Err != nil {
//			log.Err(o.Err).Str("AppID", payload.AppID).Msg(o.Err.Error())
//			return o.Err
//		}
//		appEnvironmentsToDestroy = append(appEnvironmentsToDestroy, env)
//	}
//	log.Debug().Str("AppID", app.ID).Msg("Retrieved environments to destroy")
//
//	receiptHandle := os.Getenv("RECEIPT_HANDLE")
//	if receiptHandle == "" {
//		log.Err(fmt.Errorf("no RECEIPT_HANDLE variable found")).Str("AppID", payload.AppID).Msg("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
//		// need to update the status of the environments map instead of the app
//		ue := updateEnvironmentStatusesToDestroyFailed(app, appEnvironmentsToDestroy, mm, fmt.Errorf("no RECEIPT_HANDLE variable found").Error())
//		if ue != nil {
//			log.Err(ue).Str("AppID", payload.AppID).Msg(ue.Error())
//			return ue
//		}
//		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
//	}
//
//	var accounts []types.Account
//	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
//	if o.Err != nil {
//		log.Err(o.Err).Str("AppID", payload.AppID).Msg(o.Err.Error())
//		o = mm.Update(&app, "Status", "DELETE_FAILED")
//		if o.Err != nil {
//			log.Err(o.Err).Str("AppID", payload.AppID).Msg(o.Err.Error())
//			return o.Err
//		}
//		return fmt.Errorf("an error occurred when trying to find the Master Account: %s", o.Err)
//	}
//
//	log.Debug().Str("AppID", payload.AppID).Msg("Found Master Account")
//
//	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
//		config.WithRegion(accounts[0].AwsRegion)
//		return nil
//	})
//	if err != nil {
//		log.Err(err).Str("AppID", payload.AppID).Msg(err.Error())
//		return err
//	}
//
//	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
//	if err != nil {
//		log.Err(err).Str("AppID", payload.AppID).Msg(err.Error())
//		o = mm.Update(&app, "Status", "DELETE_FAILED")
//		if o.Err != nil {
//			log.Err(o.Err).Str("AppID", payload.AppID).Msg(o.Err.Error())
//			return o.Err
//		}
//		return fmt.Errorf("an error occurred when trying to find the Do Api Key: %s", o.Err)
//	}
//
//	authResponse, err := utils.IsApiKeyValid(*doApiKey)
//	if err != nil {
//		log.Err(err).Str("AppID", payload.AppID).Msg(err.Error())
//		aco := mm.Update(&app, "Status", "APPLY_FAILED")
//		if aco.Err != nil {
//			log.Err(aco.Err).Str("AppID", payload.AppID).Msg(aco.Err.Error())
//			return aco.Err
//		}
//		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
//	}
//	if !authResponse.IsValid {
//		log.Err(fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")).Str("AppID", payload.AppID).Msg("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
//		aco := mm.Update(&app, "Status", "APPLY_FAILED")
//		if aco.Err != nil {
//			log.Err(aco.Err).Str("AppID", payload.AppID).Msg(aco.Err.Error())
//			return aco.Err
//		}
//		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
//	}
//
//	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
//		o.Region = accounts[0].AwsRegion
//	})
//
//	if !isDryRun {
//		os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")
//
//		log.Debug().Str("AppID", app.ID).Msg("Preparing Terraform")
//
//		var execPath *string
//		execPath, err = terraform.PrepareTerraform(ctx)
//		if err != nil {
//			log.Err(err).Str("AppID", app.ID).Msg(err.Error())
//			ue := updateEnvironmentStatusesToDestroyFailed(app, appEnvironmentsToDestroy, mm, err.Error())
//			if ue != nil {
//				log.Err(ue).Str("AppID", app.ID).Msg(ue.Error())
//				return ue
//			}
//			return err
//		}
//
//		log.Debug().Str("AppID", app.ID).Msg("Dry run is false. Running terraform")
//
//		err = formatWithWorkerAndDestroy(ctx, accounts[0].AwsRegion, mm, app, appEnvironmentsToDestroy, execPath)
//		if err != nil {
//			log.Err(err).Str("AppID", app.ID).Msg(err.Error())
//			ue := updateEnvironmentStatusesToDestroyFailed(app, appEnvironmentsToDestroy, mm, err.Error())
//			if ue != nil {
//				log.Err(ue).Str("AppID", app.ID).Msg(ue.Error())
//				return ue
//			}
//			return err
//		}
//	} else {
//		for _, env := range appEnvironmentsToDestroy {
//			for idx := range app.Environments {
//				if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel {
//					app.Environments[idx].Status = "DESTROYED"
//					app.Environments[idx].Endpoint = ""
//				}
//			}
//			o = mm.Save(&app)
//			if o.Err != nil {
//				log.Err(o.Err).Str("AppID", app.ID).Msg(o.Err.Error())
//				return o.Err
//			}
//			log.Debug().Str("AppID", app.ID).Msg("App environment status updated")
//		}
//	}
//
//	queueParts := strings.Split(*app.AppSqsArn, ":")
//	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])
//
//	log.Debug().Str("AppID", app.ID).Msg(fmt.Sprintf("Queue url is %s", queueUrl))
//
//	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
//		QueueUrl:      &queueUrl,
//		ReceiptHandle: &receiptHandle,
//	})
//	if err != nil {
//		log.Err(err).Str("AppID", app.ID).Msg(err.Error())
//		return err
//	}
//	// TODO delete the ECR Repo/images, OIDC repo entry from policy
//
//	o = mm.SoftDelete(&app)
//	if o.Err != nil {
//		log.Err(o.Err).Str("AppID", app.ID).Msg(o.Err.Error())
//		ue := updateEnvironmentStatusesToDestroyFailed(app, appEnvironmentsToDestroy, mm, o.Err.Error())
//		if ue != nil {
//			log.Err(ue).Str("AppID", app.ID).Msg(ue.Error())
//			return ue
//		}
//		return o.Err
//	}
//	return nil
//}
