
[![Release](https://img.shields.io/github/release/trustbloc/edv.svg?style=flat-square)](https://github.com/trustbloc/edv/releases/latest)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://raw.githubusercontent.com/trustbloc/edv/main/LICENSE)
[![Godocs](https://img.shields.io/badge/godoc-reference-blue.svg)](https://godoc.org/github.com/trustbloc/edv)

[![edv ci](https://github.com/trustbloc/edv/actions/workflows/build.yml/badge.svg)](https://github.com/trustbloc/edv/actions/workflows/build.yml)
[![codecov](https://codecov.io/gh/trustbloc/edv/branch/main/graph/badge.svg)](https://codecov.io/gh/trustbloc/edv)
[![Go Report Card](https://goreportcard.com/badge/github.com/trustbloc/edv)](https://goreportcard.com/report/github.com/trustbloc/edv)

# edv
An implementation of Encrypted Data Vaults [from the Confidential Storage 0.1 (04 December 2020) specification](https://identity.foundation/confidential-storage/). This implementation is a work in progress; be sure to read the [limitations](#limitations) section which outlines which parts of the specification have yet to be implemented.

## Limitations
The following has not yet been implemented:
* Service endpoint discovery
* Index querying with multiple name+value pairs (which is still a work in-progress in the [specification](https://identity.foundation/confidential-storage/))
* Streams (also a work in-progress in the [specification](https://identity.foundation/confidential-storage/))

## Underlying Storage
This EDV server is not by itself a database - a database provider must be chosen for it to work. This underlying database is used by the EDV server for storage of encrypted data. Currently, three database providers are supported:

- MongoDB
- CouchDB
- In-memory storage

See [here](docs/rest/edv_cli.md#edv-server-parameters) for information on how to choose the database provider.

## Extensions
This EDV server implementation includes support for a number of optional features that, as of writing, are either recently added to the spec (and not in widespread use) or are features marked "at-risk". They are all disabled by default, but they can all be safely enabled without breaking any standard features. Non-extension-aware clients will still work seamlessly. See the [extensions documentation](docs/extensions.md) for more information.

## Documentation
- [Build + BDD tests](docs/test/build.md)
- [Run as Binary with CLI](docs/rest/edv_cli.md)
- [Run as Docker Container](docs/rest/edv_docker.md)
- [OpenAPI Spec](docs/rest/openapi_spec.md)
- [OpenAPI Demo](docs/rest/openapi_demo.md)

## Contributing
Thank you for your interest in contributing. Please see our [community contribution guidelines](https://github.com/trustbloc/community/blob/main/CONTRIBUTING.md) for more information.

## License
Apache License, Version 2.0 (Apache-2.0). See the [LICENSE](LICENSE) file.