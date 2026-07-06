class Ticket:
    event = models.ForeignKey(Event, related_name='things')
