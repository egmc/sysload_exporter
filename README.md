sysload exporter
---

sysload exporter is prometheus style, go implementation of https://github.com/gree/sysload

# usage

```
usage: sysload_exporter [<flags>]

Flags:
  -h, --help                    Show context-sensitive help (also try --help-long and --help-man).
      --debug                   Debug mode.
      --info                    show current information and exit
  -b, --target-block-devices=TARGET-BLOCK-DEVICES  
                                Target block devices to track io utils
      --listen-address=":9858"  The address to listen on for HTTP requests.
      --interrupted-threshold=40.0  
                                Threshold to consider interrupted cpu usage as sysload
```
