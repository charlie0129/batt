# How is it different from XXX?

**It is free and open source**. It even comes with some features (like idle sleep prevention and pre-sleep stop charging) that are only available in paid counterparts. It comes with no ads, no tracking, no telemetry, no analytics, no bullshit. It is open source, so you can read the code and verify that it does what it says it does.

**It is simple but well-thought.** It only does charge limiting and does it well. With legacy direct control, `batt` handles sleep edge cases that can otherwise let a MacBook charge to 100%. With firmware-managed or native control, Apple retains the configured range or target during sleep. Other features are intentionally limited to keep it simple. If you encounter a problem or want an additional feature, please raise an issue.

**It is light-weight.** No electron GUIs hogging your system resources like some other tools. You can use batt on the command-line, or use the native macOS menu bar app if you prefer a GUI. The GUI is written using native macOS APIs, so it is light-weight and fast.

### But macOS has similar features built-in, doesn't it?

Yes, macOS has optimized battery charging. It will try to find out your charging and working schedule and prohibits charging above 80% for a couple of hours overnight. However, if you have an irregular schedule, this will simply not work. Also, you lose predictability (which I value a lot) about your computer's behavior, i.e., by letting macOS decide for you, you, the one who knows your schedule the best, cannot control when to charge or when not to charge.

`batt` lets you choose a maximum charge level. In native mode, macOS enforces it and can temporarily override it to charge fully.
