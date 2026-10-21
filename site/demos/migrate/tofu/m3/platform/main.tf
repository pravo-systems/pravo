terraform {
  required_providers {
    local  = { source = "hashicorp/local", version = "~> 2.5" }
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
}

locals {
  ports = {
    api    = 8080
    web    = 8081
    worker = 8082
  }
}

module "service" {
  source   = "./modules/service"
  for_each = local.ports

  name = each.key
  port = each.value
}

resource "random_password" "admin" {
  length  = 24
  special = false
}

moved {
  from = random_password.root
  to   = random_password.admin
}

output "endpoints" {
  value = sort([for svc in module.service : svc.endpoint])
}

output "admin_password" {
  value     = random_password.admin.result
  sensitive = true
}
