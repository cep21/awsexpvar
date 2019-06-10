deps:
	# Do not check in /vendor
	test ! -d 'vendor' || (echo "Failing checkout.  There is already a /vendor directory"; exit 1)
	# Using vendor-only ensures that your gopkg.lock is correct.
	# If you run into issues, execute `dep ensure` locally to populate
	# the lock file correctly
	dep ensure -vendor-only

test:
	go test -race ./...

# Easy way to reformat your code.  You should run `make fix` before you push
fix:
	find . -iname '*.go' -not -path '*/vendor/*' -print0 | xargs -0 gofmt -s -w
	find . -iname '*.go' -not -path '*/vendor/*' -print0 | xargs -0 goimports -w

lint:
	# Install is required for metalinter to work
	go install ./...
	# No parameters.  Configured with .metalinter.json file
	gometalinter ./...

clean:
	rm -rf vendor

bench:
	go test -bench . -run=$$^  ./...
