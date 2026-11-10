variable "region" {
  description = "OCI region, for example ap-mumbai-1"
  type        = string
}

variable "tenancy_ocid" {
  type = string
}

variable "user_ocid" {
  type      = string
  sensitive = true
}

variable "fingerprint" {
  type      = string
  sensitive = true
}

variable "private_key_path" {
  type      = string
  sensitive = true
}

variable "compartment_ocid" {
  type = string
}

variable "ssh_public_key" {
  type = string
}

variable "image_ocid" {
  description = "Ubuntu 22.04/24.04 ARM image OCID for the region"
  type        = string
}

variable "shape" {
  type    = string
  default = "VM.Standard.A1.Flex"
}

variable "ocpus" {
  type    = number
  default = 4
}

variable "memory_gbs" {
  type    = number
  default = 24
}

variable "admin_cidr" {
  description = "CIDR allowed to SSH, for example 203.0.113.10/32"
  type        = string
}

variable "domain" {
  description = "Public hostname; use <ip>.sslip.io for a demo"
  type        = string
}

variable "boot_volume_gbs" {
  type    = number
  default = 100
}
