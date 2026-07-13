# AI Watch UI Optimization Design

**Date:** 2026-07-13
**Status:** Approved

## Goal

Improve AI Watch as a trustworthy developer operations console, balancing visual identity with readability, efficiency, accessibility, and responsive behavior. Business behavior and API contracts remain unchanged.

## Direction: Real-Time Signal Console

The interface uses the probe signal as its central visual language. Live cyan, success green, warning amber, and danger red communicate system state across the dashboard, job flow, and live detail view. Generic glass-card decoration and ambient glow are reduced so the product feels like a focused reliability instrument rather than a themed SaaS template.

The signature element is a system pulse rail: a compact operational strip that communicates backend connectivity, active jobs, available environments, and the latest probe result. Running jobs extend this language through signal tracks and clear timing information.

## Design System

- Background: `#07111C`
- Primary surface: `#0C1926`
- Raised surface: `#122231`
- Primary text: `#F2F7FA`
- Secondary text: `#9AAEBC`
- Live signal: `#39D6C8`
- Success: `#5DD39E`
- Warning: `#F4B860`
- Danger: `#FF6B78`
- Typography: system Chinese sans-serif for UI and DM Mono for technical data
- Body copy: 14–16px; supporting text: at least 12px
- Spacing: consistent 4/8px rhythm
- Motion: 150–300ms state transitions; signal motion only when it communicates live activity; reduced-motion is respected

## Layout

The desktop application retains its sidebar, sticky top bar, dashboard, settings view, new-job drawer, and job detail view. The dashboard hierarchy changes to:

1. Page purpose and primary action.
2. System pulse rail with current operational state.
3. Scannable core metrics with fewer decorative effects.
4. Running signal tracks and environment health.
5. Recent task summaries and provider availability.

Tablet and mobile layouts reflow information instead of removing core fields. Secondary details may collapse into labeled metadata rows, but provider health, duration, attempt count, and result state remain reachable.

## Interaction and Accessibility

- Dialog-like surfaces expose dialog semantics, labelled titles, focus entry, Escape dismissal, focus containment, and focus restoration.
- Mobile navigation receives equivalent modal behavior while open.
- Icon-only controls have accessible names.
- Switches expose role, checked state, disabled state, and a minimum 44px target.
- Errors use alert semantics; loading, save, copy, connection, and task state feedback use appropriate live regions.
- Color is never the only state indicator; text and icons accompany semantic colors.
- Fixed mobile UI uses dynamic viewport units and safe-area insets.
- Keyboard focus remains highly visible across dark surfaces.

## Content and State Corrections

- Recent success rate displays an empty sample state instead of `0%` when no jobs exist.
- Environment copy reflects partial availability rather than always claiming readiness.
- Unavailable providers cannot be selected and explain why.
- Settings show whether changes are unsaved and avoid unnecessary saves.
- Browser notification denial provides a recovery explanation.
- Stop and copy failures produce visible feedback.
- Completed job details prioritize the sanitized result summary and explicitly state that live output was destroyed.

## Verification

- Build the React application with TypeScript.
- Check dashboard, new-job flow, detail view, and settings at 375px, 768px, 1024px, and 1440px.
- Verify keyboard navigation, focus trapping/restoration, Escape handling, live-region feedback, touch targets, and reduced motion.
- Confirm no secret, raw historical log, or host-configuration mutation is introduced.
