#!/bin/sh
cd "$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)" || exit 1
PROJECT="$(realpath ../..)"

_access () {
	cat > bpfink.access <<- EOF
		-:root:ALL
		#
		# User "foo" and members of netgroup "nis_group" should be
		# allowed to get access from all sources.
		# This will only work if netgroup service is available.
		#
		# User "john" should get access from ipv4 net/mask
		+:john:127.0.0.0/24
		#
		# User "john" should get access from ipv4 as ipv6 net/mask
		#+:john:::ffff:127.0.0.0/127
		#
		# User "john" should get access from ipv6 host address
		#+:john:2001:4ca0:0:101::1
		#
		# User "john" should get access from ipv6 host address (same as above)
		#+:john:2001:4ca0:0:101:0:0:0:1
		#
		# User "john" should get access from ipv6 net/mask
		#+:john:2001:4ca0:0:101::/64
		#
		# All other users should be denied to get access from all sources.
		-:ALL:ALL
	EOF
}

_passwd () {
	cat > bpfink.passwd <<- EOF
		root:x:0:0::/root:/bin/bash
		bin:x:1:1::/:/sbin/nologin
		daemon:x:2:2::/:/sbin/nologin
	EOF
}

_shadow () {
	cat > bpfink.shadow <<- 'EOF'
		root:$2y$05$67G8sQFkJR3j1JpWj71f5e29UBxuBk7WSr3Og7yUTX1wEJBWcDORm:17597::::::
	EOF
}

_config () {
	echo "bcc = \"${PROJECT}/pkg/ebpf/vfs.o\"" >> bpfink.toml
	cat >> bpfink.toml <<- 'EOF'
		level = "info"
		database = "bpfink.db"
	
		[consumers]
	EOF
	echo "root = \"${PROJECT}/examples/watcher/test-dir/\"" >> bpfink.toml

	echo "access = \"bpfink.access\"" >> bpfink.toml
	cat >> bpfink.toml <<- 'EOF'

		[consumers.users]
	EOF
	echo "passwd = \"bpfink.passwd\"" >> bpfink.toml
	echo "shadow = \"bpfink.shadow\"" >> bpfink.toml
	cat >> bpfink.toml <<- 'EOF'

		[MetricsConfig]
		graphiteHost = "127.0.0.1:3002"
		namespace = ""
		graphiteMode = "1" #1 = no logs 2 = stdout 3 = graphite server
		collectionInterval = "30s" # Seconds
		hostRolePath = "" # Path to file to identify server type
		hostRoleToken = ""
		hostRoleKey = "" # Key to look for in file
	EOF
}

init () {
	mkdir test-dir
	cd test-dir
	_passwd
	_shadow
	_access
	_config
	make -r -C "${PROJECT}/pkg/ebpf" || exit
}

run_test () {
	printf "\n\nwaiting for bpfink to start\n\n"
	sleep 3

	##Access
	printf "adding '+:nobody:nobody' to bpfink.access\n"
	echo "+:nobody:nobody">> bpfink.access
	sleep 2
	printf "\n\nremove last addition\n"
	sed -i '$ d' bpfink.access
	sleep 2

	##Shadow
	printf "\n\nadding 'RealUser:badPassword:17597::::::' to bpfink.shadow\n"
	echo "RealUser:badPassword:17597::::::" >> bpfink.shadow
	sleep 2

	##Passwd
	printf "\n\nadding 'RealUser:x:0:0::/root:/bin/bash' and 'serviceAccount:x:1:1::/:/sbin/nologin' to bpfink.passwd\n\n"
	echo "RealUser:x:4:4::/root:/bin/bash" >> bpfink.passwd
	echo "serviceAccount:x:3:3::/:/sbin/nologin" >> bpfink.passwd
	sleep 2
	printf "\n\ncleaning up bpfink.showdow and bpfink.passwd\n\n"
	sed -i '$ d' bpfink.passwd
	sed -i '$ d' bpfink.passwd
	sed -i '$ d' bpfink.shadow
	sleep 2

	##Future examples
	
	##Wrap up
	printf "\n\nGuided examples now over. Feel free to try modifying the above example files your self\n"
	printf "all monitored files can be found in test-dir, which will be cleaned up when stoping this process\n"
	printf "To quit use ctrl c\n\n"
}

run () {
	clean
	init
	run_test&
	sudo go run ${PROJECT}/cmd/main.go  --config ${PROJECT}/examples/watcher/test-dir/bpfink.toml 
	clean
}

clean () {
	rm -rf test-dir/
	rm -rf bpfink.*
}


run
