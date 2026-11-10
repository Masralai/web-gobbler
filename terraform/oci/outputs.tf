output "public_ip" {
  value = oci_core_instance.main.public_ip
}

output "ssh_command" {
  value = "ssh ubuntu@${oci_core_instance.main.public_ip}"
}

output "demo_url" {
  value = "https://${var.domain}"
}
