# How is it different from XXX?

**It is free and opensource**. It even comes with some features (like idle sleep preventions and pre-sleep stop charging) that are only available in paid counterparts. It comes with no ads, no tracking, no telemetry, no analytics, no bullshit. It is open source, so you can read the code and verify that it does what it says it does.

**It is simple but well-thought.** It only does charge limiting and does it well. On firmware < `20xxx`, `batt` handles sleep edge cases that can otherwise let a MacBook charge to 100%. On newer firmware, it delegates the range to Apple SMC so enforcement continues during sleep. Other features are intentionally limited to keep it simple. If you encounter a problem or want an additional feature, please raise an issue.

**It is light-weight.** No electron GUIs hogging your system resources like some other tools. You can use batt on the command-line, or use the native macOS menubar app if you prefer a GUI. The GUI is written using native macOS APIs, so it is light-weight and fast.

### But macOS has similar features built-in, is it?

Yes, macOS has optimized battery charging. It will try to find out your charging and working schedule and prohibits charging above 80% for a couple of hours overnight. However, if you have an un-regular schedule, this will simply not work. Also, you lose predictability (which I value a lot) about your computer's behavior, i.e., by letting macOS decide for you, you, the one who knows your schedule the best, cannot control when to charge or when not to charge.

`batt` can make sure your computer does exactly what you want. You can set a maximum charge level, and it will stop charging when the battery reaches that level. Therefore, it is recommended to disable macOS's optimized charging when using `batt`.