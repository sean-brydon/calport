import { useEffect, useRef } from "react";

import { toastManager } from "@/components/ui/toast";
import { type BoxStatus, calport, inApp } from "@/lib/calport";

// useBoxUpgrades offers, once per box per launch, to upgrade a box whose
// calportd is older than the one this app ships, as after an app update.
export function useBoxUpgrades(boxes: BoxStatus[], onUpgraded: () => void) {
  const asked = useRef(new Set<string>());
  useEffect(() => {
    if (!inApp) return;
    for (const box of boxes) {
      if (box.state !== "online" || asked.current.has(box.name)) continue;
      asked.current.add(box.name);
      calport.upgradeCheck(box.name).then(
        (c) => {
          if (!c.outdated) return;
          toastManager.add({
            title: `${box.name} runs an older calportd`,
            description: "Upgrading takes seconds, needs no SSH, and keeps sessions running.",
            type: "info",
            timeout: 0,
            actionProps: {
              children: "Upgrade",
              onClick: async () => {
                const id = toastManager.add({ title: `Upgrading ${box.name}…`, type: "loading" });
                try {
                  await calport.upgrade(box.name);
                  toastManager.update(id, { title: `${box.name} is up to date`, type: "success" });
                  onUpgraded();
                } catch (err) {
                  toastManager.update(id, { title: `Could not upgrade ${box.name}`, description: err instanceof Error ? err.message : String(err), type: "error" });
                }
              },
            },
          });
        },
        () => {
          // A box that cannot say its build is reported by its own checks.
        },
      );
    }
  }, [boxes, onUpgraded]);
}
