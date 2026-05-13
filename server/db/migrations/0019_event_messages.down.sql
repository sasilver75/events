DROP TRIGGER  IF EXISTS event_messages_notify_after_insert ON public.event_messages;
DROP FUNCTION IF EXISTS public.event_messages_notify();
DROP TABLE    IF EXISTS public.event_messages;
