#!/bin/bash
#
# osc-kata-addons-install.sh
# Install addon artifacts (kernel) from container images
# Reuses functions from lib.sh
#

set -e

# Source the shared library functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

#######################################
# Update the configuration file
# Arguments:
#   $1: kernel path (empty if not installed)
# Returns:
#   0 on success
#######################################
update_config() {
    local kernel_path="$1"
    
    local config_file="/etc/kata-containers/kata-se/configuration.toml"
    
    if [ ! -f  chroot /host "$config_file" ]; then
        echo "Warning: Configuration file not found: $config_file"
        return 1
    fi
    
    # Update kernel if provided
	chroot /host sed -i "s|^\(kernel[[:space:]]*=[[:space:]]*\)\".*\"|\1\"$kernel_path\"|g" "$config_file"
	echo "  Updated kernel: $kernel_path"

    return 0
}

#######################################
# Install addon artifacts
# Reads from environment variables
# Returns:
#   0 on success
#######################################
install_addons() {
    local addon_image="${ADDON_IMAGE:?}"
    
    echo "Installing addon artifacts from: $addon_image"
    
    local kernel_src="${ADDON_KERNEL_PATH:?}"

    if [ -z "$kernel_src" ]; then
        echo "ERROR: ADDON_KERNEL_PATH is mandatory but was not provided." >&2
        exit 1
    fi
    
    # Standard installation directory
    local install_dir="/host/etc/kata-containers"
    local temp_dir="/tmp/kata-addons-$$"
    local auth_file="/tmp/regauth/auth.json"
    
    mkdir -p "$install_dir"
    mkdir -p "$temp_dir"
    
    local kernel_installed=""
    local kernel_path=""
    
    # Extract and install kernel
	echo "Extracting kernel from: $kernel_src"
	
	# Reuse extract_container_image from lib.sh
	if extract_container_image "$addon_image" "$kernel_src" "$temp_dir" "$auth_file"; then
		local kernel_file=$(basename "$kernel_src")
		if [ -f "$temp_dir/$kernel_file" ]; then
			kernel_installed="$install_dir/$kernel_file"
			kernel_path="/etc/kata-containers/$kernel_file"
			cp "$temp_dir/$kernel_file" "$kernel_installed"
			chmod 644 "$kernel_installed"
			echo "Kernel installed: $kernel_installed"

			# Print checksum of the installed kernel
			echo "Kernel image $kernel_file checksum (sha256):"
			sha256sum "$kernel_installed"
		fi
	fi

    
    # Cleanup
    rm -rf "$temp_dir"
    
    update_config "$kernel_path"

    echo "Addon installation completed"
    return 0
}

#######################################
# Install CoCo initrds from container image
# Reads COCO_INITRD_IMAGE and COCO_INITRD_PATH env vars
# Extracts the initrds tarball and unpacks it to
# /host/usr/share/kata-containers/ so symlinks
# like kata-cc.initrd land at the expected location
# Returns:
#   0 on success
#######################################
install_coco_initrds() {
    local coco_image="${COCO_INITRD_IMAGE:?COCO_INITRD_IMAGE env var must be set}"
    local coco_tarball_path="${COCO_INITRD_PATH:?COCO_INITRD_PATH env var must be set}"

    echo "Installing CoCo initrds from: $coco_image"

    local temp_dir="/tmp/coco-initrds-$$"
    local auth_file="/tmp/regauth/auth.json"

    mkdir -p "$temp_dir"
    mkdir -p "/host/usr/share/kata-containers"

    local tarball_name
    tarball_name=$(basename "$coco_tarball_path")

    echo "Extracting $coco_tarball_path from image"
    if extract_container_image "$coco_image" "$coco_tarball_path" "$temp_dir" "$auth_file"; then
        if [ -f "$temp_dir/$tarball_name" ]; then
            echo "Unpacking $tarball_name"
            tar xzf "$temp_dir/$tarball_name" -C /host/

            echo "CoCo initrds installed. Contents:"
            ls -la /host/usr/share/kata-containers/osbuilder-images/ 2>/dev/null || true

            if [ -L "/host/usr/share/kata-containers/osbuilder-images/kata-cc.initrd" ]; then
                echo "Verified: kata-cc.initrd symlink present"
            else
                echo "WARNING: kata-cc.initrd symlink not found after extraction"
            fi
        else
            echo "ERROR: Tarball $tarball_name not found after extraction" >&2
            rm -rf "$temp_dir"
            return 1
        fi
    else
        echo "ERROR: Failed to extract CoCo initrd image" >&2
        rm -rf "$temp_dir"
        return 1
    fi

    rm -rf "$temp_dir"

    echo "CoCo initrd installation completed"
    return 0
}

#######################################
# Uninstall CoCo initrds
# Returns:
#   0 on success
#######################################
uninstall_coco_initrds() {
    local initrd_dir="/usr/share/kata-containers/osbuilder-images"
    echo "Uninstalling CoCo initrd artifacts"

    chroot /host bash -c "
        cd '$initrd_dir' 2>/dev/null || exit 0
        rm -f kata-cc.initrd kata-cc.kernel
        rm -f kata-cc-nvidia-gpu.initrd kata-cc-nvidia-gpu.kernel
        rm -f kata-nvidia-gpu.initrd kata-nvidia-gpu.kernel
    "

    echo "CoCo initrd artifacts uninstalled"
    return 0
}

#######################################
# Uninstall addon artifacts
# Returns:
#   0 on success
#######################################
uninstall_addons() {
    local install_dir="/etc/kata-containers"
    echo "Uninstalling addon artifacts"
    
    local kernel_src="${ADDON_KERNEL_PATH:?}"
    # Remove installed artifacts
    chroot /host rm -f "$install_dir/$(basename "$kernel_src")"
    
    echo "Addon artifacts uninstalled"
    return 0
}

# Main execution
action=${1:-install}

case "$action" in
    install)
        install_addons
        ;;
    uninstall)
        uninstall_addons
        ;;
    install-initrds)
        install_coco_initrds
        ;;
    uninstall-initrds)
        uninstall_coco_initrds
        ;;
    *)
        echo "Usage: $0 {install|uninstall|install-initrds|uninstall-initrds}"
        exit 1
        ;;
esac
