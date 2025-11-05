# STACK Architecture Documentation

**Version:** v0.2 - Go/Alpaca  
**Last Updated:** October 24, 2025

## Overview

This directory contains the complete architecture documentation for the STACK platform backend, organized into focused, manageable sections.

## Document Index

1. **[Overview](./00-overview.md)** - Introduction, high-level architecture, and design patterns
2. **[Tech Stack](./01-tech-stack.md)** - Technologies, versions, and infrastructure choices
3. **[Data Models](./02-data-models.md)** - Core entities, attributes, and relationships
4. **[Components](./03-components.md)** - Service modules and their responsibilities
5. **[Workflows](./04-workflows.md)** - Sequence diagrams for critical user journeys
6. **[Database Schema](./05-database-schema.md)** - Complete DDL and schema design
7. **[Source Tree](./06-source-tree.md)** - Project structure and organization
8. **[Infrastructure](./07-infrastructure.md)** - Deployment strategy and cloud setup
9. **[Error Handling](./08-error-handling.md)** - Error handling patterns and logging
10. **[Coding Standards](./09-coding-standards.md)** - Mandatory coding conventions
11. **[Test Strategy](./10-test-strategy.md)** - Testing approach and standards

## Quick Navigation

### For Developers
- Start with [Overview](./00-overview.md) for the big picture
- Review [Coding Standards](./09-coding-standards.md) before writing code
- Check [Components](./03-components.md) to understand module boundaries
- Reference [Data Models](./02-data-models.md) for database entities

### For Architects
- [Overview](./00-overview.md) - Architectural patterns and decisions
- [Tech Stack](./01-tech-stack.md) - Technology choices and rationale
- [Infrastructure](./07-infrastructure.md) - Deployment and scaling strategy
- [Workflows](./04-workflows.md) - Critical system flows

### For DevOps
- [Infrastructure](./07-infrastructure.md) - AWS setup and IaC
- [Source Tree](./06-source-tree.md) - Repository structure
- [Database Schema](./05-database-schema.md) - Schema migrations

### For QA/Testing
- [Test Strategy](./10-test-strategy.md) - Testing approach and requirements
- [Workflows](./04-workflows.md) - Expected system behavior
- [Error Handling](./08-error-handling.md) - Error scenarios

## Change Log

| Date | Version | Description | Author |
| :--- | :--- | :--- | :--- |
| Oct 24, 2025 | v0.2 | Sharded architecture into multiple focused documents | John (PM) |
| Oct 24, 2025 | v0.2 | Complete rewrite for Go, Alpaca, and Circle pivot | Winston |
| Sept 27, 2025 | v0.1 | Initial NestJS architecture | Winston |

## Related Documentation

- **[PRD Documentation](../prd/)** - Product requirements and business context
- **[README](../../README.md)** - Project overview and quick start guide

---

**Note:** This architecture documentation is the source of truth for backend implementation. All code generation and development decisions should align with these specifications.
