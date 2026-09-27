import { GlobeLockIcon, PlusIcon, ServerIcon, SettingsIcon } from "lucide-react";

import { StateDot } from "@/components/state-dot";
import { Button } from "@/components/ui/button";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import type { BoxStatus, Network } from "@/lib/calport";

interface AppSidebarProps {
  boxes: BoxStatus[];
  networks: Network[];
  selected: string | undefined;
  settings: boolean;
  onSelect: (box: string) => void;
  onSettings: () => void;
  onAddBox: () => void;
}

export function AppSidebar({ boxes, networks, selected, settings, onSelect, onSettings, onAddBox }: AppSidebarProps) {
  return (
    <Sidebar>
      <SidebarHeader>
        <div className="flex items-center gap-2 px-2 py-1.5">
          <img src="/favicon.svg" alt="" className="size-5" />
          <span className="font-heading text-base font-semibold">Calport</span>
        </div>
      </SidebarHeader>
      <SidebarContent className="overscroll-none">
        <SidebarGroup>
          <SidebarGroupLabel>Boxes</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {boxes.map((box) => (
                <SidebarMenuItem key={box.name}>
                  <SidebarMenuButton isActive={box.name === selected} onClick={() => onSelect(box.name)}>
                    <ServerIcon />
                    <span>{box.name}</span>
                  </SidebarMenuButton>
                  <SidebarMenuBadge className="gap-1.5 tabular-nums">
                    {box.state === "online" && box.latency_ms ? `${box.latency_ms}ms` : null}
                    <StateDot state={box.state} />
                  </SidebarMenuBadge>
                </SidebarMenuItem>
              ))}
              {boxes.length === 0 && (
                <p className="px-2 py-1.5 text-muted-foreground text-sm">No boxes paired yet.</p>
              )}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        {networks.length > 0 && (
          <SidebarGroup>
            <SidebarGroupLabel>Networks</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {networks.map((n) => (
                  <SidebarMenuItem key={n.name}>
                    <SidebarMenuButton render={<div />} className="cursor-default">
                      <GlobeLockIcon />
                      <span>{n.name}</span>
                    </SidebarMenuButton>
                    <SidebarMenuBadge className="text-muted-foreground">{n.tailnet ?? n.state}</SidebarMenuBadge>
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        )}
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton isActive={settings} onClick={onSettings}>
              <SettingsIcon />
              <span>Settings</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
        <Button variant="outline" className="w-full" onClick={onAddBox}>
          <PlusIcon />
          Add box
        </Button>
      </SidebarFooter>
    </Sidebar>
  );
}
