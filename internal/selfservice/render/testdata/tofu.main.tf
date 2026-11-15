module "main" {
  source  = "registry.example.com/platform/postgres/aws"
  version = "2.4.1"

  backup_retention_days = var.backup_retention_days
  instance_size = var.instance_size
  network_id = var.network_id
  storage_gb = var.storage_gb
}

variable "backup_retention_days" {}

variable "instance_size" {}

variable "network_id" {}

variable "storage_gb" {}
