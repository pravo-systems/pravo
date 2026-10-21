resource "random_integer" "port" {
  count = 2
  min   = 8000
  max   = 8999
}

resource "random_pet" "region" {
  for_each = toset(var.regions)
  prefix   = each.key
  length   = 2
}

variable "create_extra" {
  type    = bool
  default = false
}

resource "random_pet" "extra" {
  count  = var.create_extra ? 1 : 0
  length = 1
}
