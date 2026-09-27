import { ConnectBox } from "@/components/connect-box";
import { Dialog, DialogDescription, DialogHeader, DialogPanel, DialogPopup, DialogTitle } from "@/components/ui/dialog";
import type { Network } from "@/lib/calport";

interface AddBoxDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  networks: Network[];
  onNetworksChanged: () => void;
  onConnected: (box: string) => void;
}

export function AddBoxDialog({ open, onOpenChange, networks, onNetworksChanged, onConnected }: AddBoxDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogPopup className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Add a box</DialogTitle>
          <DialogDescription>Connect another machine where your code runs.</DialogDescription>
        </DialogHeader>
        <DialogPanel>
          <ConnectBox
            networks={networks}
            onNetworksChanged={onNetworksChanged}
            onConnected={(box) => {
              onConnected(box);
              onOpenChange(false);
            }}
          />
        </DialogPanel>
      </DialogPopup>
    </Dialog>
  );
}
