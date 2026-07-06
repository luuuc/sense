def redeemed(event):
    return event.things.filter(redeemed=True)
