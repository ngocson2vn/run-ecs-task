# batch_submitter
A program for submitting a job to AWS Batch

# How to deploy to jenkins slaves
## build job
https://staging-batch.finc.com/job/batch_submitter_jobs/job/build/

Build go script by this job. This job kicks next job "copy_to_master"

## copy to master job
https://staging-batch.finc.com/job/batch_submitter_jobs/job/copy_to_master/

This job copies batch_submitter binary to master node.

## check_slave job
https://staging-batch.finc.com/job/batch_submitter_jobs/job/check_slave_i-041e0e16e558f9eb0/

This job copies batch_submitter binary to each slaves. Each slaves need this job.
