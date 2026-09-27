#!/usr/bin/env bash
# Convert the MANIFEST's `takenAt` stamp to a UTC epoch second, portably.
#
# The backup scripts read `date -u -d "$TAKEN_AT"`, which is GNU coreutils
# only. macOS ships BSD date, where `-d` means daylight-saving time and the
# conversion fails: the caller sees an empty epoch, treats the age as unknown,
# and carries on. For verify-backup.sh that is the silent failure it exists to
# catch, passing a backup that has not been refreshed in a year. A digest has
# the same portability story (see lib-sha256.sh) and is already handled there.
#
# The stamp has one shape, `date -u +%Y-%m-%dT%H:%M:%SZ`, fixed width, so
# parsing is a regex and the conversion is integer arithmetic on the calendar
# rather than a call out to `date`. Nothing here shells out, so the answer does
# not depend on which `date` the host happens to have.
#
# Sourced, not executed. Callers do:
#   . "$(dirname "${BASH_SOURCE[0]}")/lib-timestamp.sh"
#   TAKEN_EPOCH="$(iso8601_to_epoch "$TAKEN_AT")"   # non-zero if unparseable

# iso8601_to_epoch prints the UTC epoch second for a YYYY-MM-DDTHH:MM:SSZ
# stamp, and returns non-zero for anything else, including an empty string.
# A caller that cannot parse the age must not read it as age zero.
iso8601_to_epoch() {
  local stamp="${1:-}"
  local year month day hour minute second

  if [[ ! "$stamp" =~ ^([0-9]{4})-([0-9]{2})-([0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2})Z$ ]]; then
    return 1
  fi
  year="${BASH_REMATCH[1]}"
  month="${BASH_REMATCH[2]}"
  day="${BASH_REMATCH[3]}"
  hour="${BASH_REMATCH[4]}"
  minute="${BASH_REMATCH[5]}"
  second="${BASH_REMATCH[6]}"

  if ((10#$month < 1 || 10#$month > 12)); then
    return 1
  fi
  # Days per month, leap years included. Rejects 2026-02-30, which the
  # arithmetic below would happily roll over into March.
  local month_days=(31 28 31 30 31 30 31 31 30 31 30 31)
  local leap=0
  if (( (10#$year % 4 == 0 && 10#$year % 100 != 0) || 10#$year % 400 == 0 )); then
    leap=1
  fi
  local max_day="${month_days[10#$month - 1]}"
  (( month_days[10#$month - 1] == 28 && leap == 1 )) && max_day=29
  if ((10#$day < 1 || 10#$day > max_day)); then
    return 1
  fi
  if ((10#$hour > 23 || 10#$minute > 59 || 10#$second > 60)); then
    return 1
  fi

  # Days from 1970-01-01 (Howard Hinnant's civil-from-days, inverted). Pure
  # integer arithmetic, so it is exact for every date backup.sh can produce.
  local y=$((10#$year))
  local m=$((10#$month))
  local d=$((10#$day - 1))
  (( m <= 2 )) && y=$((y - 1))
  local era=$(( (y >= 0 ? y : y - 399) / 400 ))
  local yoe=$((y - era * 400))
  local mp=$(( m + (m > 2 ? -3 : 9) ))
  local doy=$(( (153 * mp + 2) / 5 + d ))
  local doe=$(( yoe * 365 + yoe / 4 - yoe / 100 + doy ))
  local days=$(( era * 146097 + doe - 719468 ))

  echo $(( days * 86400 + 10#$hour * 3600 + 10#$minute * 60 + 10#$second ))
}
