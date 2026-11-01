#!/bin/bash
# maps the new flag --installation to the old name --region (renamed in #2002); everything else passes through
a=(); for x in "$@"; do [ "$x" = "--installation" ] && x="--region"; a+=("$x"); done
exec /tmp/bin-old "${a[@]}"
