# DragonOps Orchestrator

The orchestrator is responsible for managing the execution of the various tasks that are required to deploy 
DragonOps infrastructure. It is written in Golang and runs as an ECS task in the `dragonops` cluster. 

It is open source for visibility into how DragonOps works and what is being executed in client accounts.

## Local Development
If you are developing locally and plan to test in AWS, CI/CD will take care of all of the below.

If you need to test your changes locally, you need to do a few things first.

* Set the `AWS_PROFILE` environment variable to the profile you want to use. This profile should match the profile of the
AWS Account you are using to test (ie, the "client" account).
* If you use sso, run `aws sso login --profile <my_profile>`
* In the `worker` repo (where templates are stored) you need to follow the instructions in the Readme. Then:
  * `cp ../path/to/worker/tmpl.tgz.age app`
  * `cp ../path/to/worker/worker app`
* In your code editor/IDE, you will need a few environment variables set:
```bash
 AWS_REGION=us-east-1
 DRAGONOPS_API=https://api.dev.dragonops.io
 IS_LOCAL=true
 MESSAGE={"app_id": "your-app-id", "environment_ids": ["your-env-id","your-env-id-2"], "job_id": "your-job-id", "job_name": "app apply", "region": "us-east-1", "user_name": "your-do-username"}
 RECEIPT_HANDLE=blah # can be any value
```
* For the `MESSAGE` env var:
    * `job_id` can be any randomized string value
    * `job_name` should be one of app apply, group apply , app destroy, etc
    * `region` is the AWS region you are working in
    * `user_name` is your DO username - this can be found in secrets manager in the AWS Account you are working in.
* For go tool arguments section, you will need to add:
  * `-ldflags "-X main.bugSnagDevKey=<bug_snag_dev_key>"` --> Ask someone on your team for the bug snag dev key

>Note: These instructions are a WIP. Please provide any feedback or clarifications you find to the dragonops team so we can improve them.

Test