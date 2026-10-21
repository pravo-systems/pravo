terraform {
  required_providers {
    random = { source = "hashicorp/random", version = "~> 3.6" }
    local  = { source = "hashicorp/local", version = "~> 2.5" }
  }
}

provider "local" {
  alias = "archive"
}

module "notes" {
  source    = "./modules/notes"
  providers = { local = local.archive }
  dir       = "out/notes"
  names     = ["alpha", "beta"]
}
