output "server_name" {
  value = random_pet.server.id
}

output "note_paths" {
  value = module.notes.paths
}

output "port_numbers" {
  value     = random_integer.port[*].result
  sensitive = true
}
