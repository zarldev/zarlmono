# zenv

`zenv` provides a simple, lightweight mechanism for accessing environment variables.

## Purpose and Limitations

This package is designed for applications where configuration is straightforward and can be managed entirely through the environment. It is a deliberate choice for simplicity in cloud-native or containerized deployments where environment variables are a standard configuration practice.

Every accessor (`Get`, `String`, `Int`, `Int64`, `Float64`, `Duration`, `Bool`) takes a default value that is returned when the variable is unset or fails to parse.

As noted in our project architecture review, `zenv` is not a comprehensive configuration solution. It does not support:

- Hierarchical configuration (e.g., from YAML, TOML, or JSON files)
- Strict validation — parse failures silently fall back to the default rather than erroring

For more complex applications, we recommend evaluating a more robust configuration library that can handle hierarchical data and provide more flexibility. Environment variables can still be used to override file-based settings in such a setup.
