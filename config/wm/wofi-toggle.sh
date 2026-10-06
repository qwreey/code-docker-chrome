#!/bin/sh
# wofi has no single-instance mode of its own, and it never closes on focus loss
# here because the click that takes focus away lands on waybar's own layer
# surface rather than a real window — so every click of the "☰ Menu" button just
# stacked another identical launcher on top of the last one.
#
# Toggle instead: a click while one is already open closes it, which is what a
# normal start-menu button does anyway.
pkill -x wofi || exec wofi --show drun
