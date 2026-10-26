# TODO

## Multiple repositories

Watch several repositories in one stream.

- Repeatable `-R`, and maybe `--org` for every repository in an organization
- One set of sources per repository, merged by the existing poll loop
- A `REPO` column in the table when more than one repository is watched
- Costs 2 to 3 requests per repository per poll, all 304 when idle
- A config file at `$XDG_CONFIG_HOME/gh-tail/config.yml` only once repository lists get too long to type
